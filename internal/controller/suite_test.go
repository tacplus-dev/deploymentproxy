package controller

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/yaml"

	deploymentproxyv1 "github.com/tacplus-dev/deploymentproxy/api/v1"
	"github.com/tacplus-dev/deploymentproxy/internal/config"
)

// The suite runs a real API server (envtest) with the repository's CRD, and runs the controller as
// deploymentproxy's service account under the repository's RBAC manifests, so a permission missing from them, or
// one it relies on that they do not grant, fails here.

const testConfig = `
reservedTids: ["00000001", "00000002"]
sharedTids: ["00000002"]
quota:
  default:
    pods: "10"
  deployments:
    big:
      pods: "100"
`

// resync is the test manager's resync period: short, so that a config change reaches existing tenant
// namespaces within a test, the way it does within deploymentproxy's (longer) resync period.
const resync = 2 * time.Second

var (
	scheme   = runtime.NewScheme()
	adminCfg *rest.Config
	admin    client.Client
	// currentConfig is what the controller reads on every reconcile; setConfig changes it.
	currentConfig atomic.Pointer[config.Config]
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	log.SetLogger(zap.New(zap.UseDevMode(true), zap.WriteTo(os.Stderr)))
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if err := deploymentproxyv1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		fmt.Fprintln(os.Stderr, "KUBEBUILDER_ASSETS is not set: run `make test`")
		return 1
	}

	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd")},
		ErrorIfCRDPathMissing: true,
	}
	var err error
	if adminCfg, err = env.Start(); err != nil {
		panic(err)
	}
	defer func() { _ = env.Stop() }()
	if admin, err = client.New(adminCfg, client.Options{Scheme: scheme}); err != nil {
		panic(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, f := range []string{"manager/namespace.yaml", "rbac/service_account.yaml", "rbac/cluster_role.yaml", "rbac/cluster_role_binding.yaml"} {
		if err := applyFile(ctx, admin, filepath.Join("..", "..", "config", f), nil); err != nil {
			panic(err)
		}
	}

	// envtest runs no ClusterRole aggregation, so admin is empty and binding it needs no right at all.
	// Give it rules, as a cluster does, so that deploymentproxy needs its bind grant.
	adminRole := &rbacv1.ClusterRole{}
	if err := admin.Get(ctx, client.ObjectKey{Name: "admin"}, adminRole); err != nil {
		panic(err)
	}
	adminRole.Rules = []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"pods", "secrets", "services", "configmaps"}, Verbs: []string{"*"}}}
	if err := admin.Update(ctx, adminRole); err != nil {
		panic(err)
	}

	cfg, err := config.Parse([]byte(testConfig))
	if err != nil {
		panic(err)
	}
	currentConfig.Store(cfg)
	cacheOpts := CacheOptions()
	syncPeriod := resync
	cacheOpts.SyncPeriod = &syncPeriod
	mgr, err := ctrl.NewManager(impersonate(adminCfg, "deploymentproxy", "deploymentproxy"), ctrl.Options{
		Scheme:  scheme,
		Cache:   cacheOpts,
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	if err != nil {
		panic(err)
	}
	r := &Reconciler{
		Client:    mgr.GetClient(),
		APIReader: mgr.GetAPIReader(),
		Config:    func() (*config.Config, error) { return currentConfig.Load(), nil },
	}
	if err := r.SetupWithManager(mgr); err != nil {
		panic(err)
	}
	pf := &PodFaultReconciler{
		Client:    mgr.GetClient(),
		APIReader: mgr.GetAPIReader(),
		Config:    func() (*config.Config, error) { return currentConfig.Load(), nil },
	}
	if err := pf.SetupWithManager(mgr); err != nil {
		panic(err)
	}
	go func() {
		if err := mgr.Start(ctx); err != nil {
			panic(err)
		}
	}()
	return m.Run()
}

// setConfig makes the controller read config.yaml content c from now on, and testConfig again once the test
// is over.
func setConfig(t *testing.T, c string) {
	t.Helper()
	cfg, err := config.Parse([]byte(c))
	if err != nil {
		t.Fatal(err)
	}
	old := currentConfig.Swap(cfg)
	t.Cleanup(func() { currentConfig.Store(old) })
}

// impersonate returns cfg acting as the service account namespace/name.
func impersonate(cfg *rest.Config, namespace, name string) *rest.Config {
	c := rest.CopyConfig(cfg)
	c.Impersonate = rest.ImpersonationConfig{
		UserName: "system:serviceaccount:" + namespace + ":" + name,
		Groups:   []string{"system:serviceaccounts", "system:serviceaccounts:" + namespace, "system:authenticated"},
	}
	return c
}

// applyFile creates every object of a multi-document YAML manifest, after replacing each key of vars.
func applyFile(ctx context.Context, c client.Client, path string, vars map[string]string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for k, v := range vars {
		b = bytes.ReplaceAll(b, []byte(k), []byte(v))
	}
	for _, doc := range strings.Split(string(b), "\n---") {
		u := &unstructured.Unstructured{}
		if err := yaml.Unmarshal([]byte(doc), &u.Object); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if len(u.Object) == 0 {
			continue
		}
		if err := c.Create(ctx, u); err != nil {
			return fmt.Errorf("%s: %s %s: %w", path, u.GetKind(), u.GetName(), err)
		}
	}
	return nil
}

// eventually retries f until it returns nil, failing the test after 30 seconds.
func eventually(t *testing.T, f func() error) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		err := f()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// consistently fails the test if f returns an error at any point in the next 2 seconds.
func consistently(t *testing.T, f func() error) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := f(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// deploymentNamespace creates a namespace as the top pipeline does: labelled with its deployment (unless
// deployment is empty).
func deploymentNamespace(t *testing.T, name, deployment string) {
	t.Helper()
	ns := &corev1.Namespace{}
	ns.Name = name
	if deployment != "" {
		ns.Labels = map[string]string{DeploymentLabel: deployment}
	}
	if err := admin.Create(context.Background(), ns); err != nil {
		t.Fatal(err)
	}
}
