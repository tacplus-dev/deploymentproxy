package config

import (
	"fmt"
	"os"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/yaml"
)

const sample = `
reservedTids: ["00000001"]
quota:
  default:
    pods: "50"
    requests.cpu: "4"
  deployments:
    prod:
      pods: "200"
      services.loadbalancers: "3"
`

func TestValidTid(t *testing.T) {
	c, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	for tid, want := range map[string]bool{
		"a1b2c3d4":  true,
		"abcdefgh":  true,
		"0000000a":  true,
		"00000001":  true,  // reserved and configured
		"00000002":  false, // reserved range, not configured
		"12345678":  false,
		"abc":       false,
		"abcdefghi": false,
		"ABCDEFGH":  false,
		"abcd-fgh":  false,
		"":          false,
	} {
		if got := c.ValidTid(tid); got != want {
			t.Errorf("ValidTid(%q) = %v, want %v", tid, got, want)
		}
	}
}

func TestQuotaFor(t *testing.T) {
	c, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	check := func(hard corev1.ResourceList, name corev1.ResourceName, want string) {
		t.Helper()
		got, ok := hard[name]
		if !ok || got.Cmp(resource.MustParse(want)) != 0 {
			t.Errorf("%s = %v, want %s", name, got.String(), want)
		}
	}
	dev := c.QuotaFor("dev")
	check(dev, corev1.ResourcePods, "50")
	check(dev, corev1.ResourceRequestsCPU, "4")
	check(dev, corev1.ResourceServicesLoadBalancers, "0")
	check(dev, corev1.ResourceServicesNodePorts, "0")

	prod := c.QuotaFor("prod")
	check(prod, corev1.ResourcePods, "200")
	check(prod, corev1.ResourceRequestsCPU, "4")
	// No override lifts the ban on load balancers and node ports.
	check(prod, corev1.ResourceServicesLoadBalancers, "0")
	check(prod, corev1.ResourceServicesNodePorts, "0")
}

func TestParseRejects(t *testing.T) {
	for _, bad := range []string{
		`reservedTids: ["0000001a"]`,
		`reservedTids: ["1"]`,
		`unknown: 1`,
		`quota: {default: {pods: "lots"}}`,
		`sharedTids: ["00000002"]`, // shared, not reserved
		`{reservedTids: ["00000002"], sharedTids: ["abcdefgh"]}`,
		// A reader is a User or a Group, never a service account (of another namespace or any).
		`readers: {deployments: {prod: [{kind: ServiceAccount, name: operator, namespace: tac-other}]}}`,
		`readers: {deployments: {prod: [{kind: ServiceAccount, name: operator}]}}`,
		`readers: {deployments: {prod: [{kind: Robot, name: r2}]}}`,
		`readers: {deployments: {prod: [{kind: User, name: ""}]}}`,
		`readers: {deployments: {prod: [{kind: User, name: a@example.com, namespace: tac-prod}]}}`,
		`readers: {deployments: {prod: [{kind: Group, name: devs, apiGroup: example.com}]}}`,
		`readers: {deployments: {prod: [{kind: User, name: a@example.com, unknown: 1}]}}`,
		`readers: {unknown: 1}`,
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("Parse(%q) succeeded, want an error", bad)
		}
	}
}

func TestShippedConfigMap(t *testing.T) {
	b, err := os.ReadFile("../../config/manager/configmap.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cm := &corev1.ConfigMap{}
	if err := yaml.Unmarshal(b, cm); err != nil {
		t.Fatal(err)
	}
	c, err := Parse([]byte(cm.Data["config.yaml"]))
	if err != nil {
		t.Fatal(err)
	}
	if !c.ValidTid("00000001") {
		t.Error("people's tid 00000001 is not reserved")
	}
	if !c.ValidTid("00000002") {
		t.Error("restore's tid 00000002 is not reserved")
	}
}

func TestChaos(t *testing.T) {
	c, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if c.Chaos.FailureImage != DefaultFailureImage {
		t.Errorf("failureImage = %q, want the default %q", c.Chaos.FailureImage, DefaultFailureImage)
	}
	c, err = Parse([]byte(`{chaos: {excludedNamespaces: [tac-prod-quiet], excludedServices: [kernel], failureImage: example.com/pause:1}}`))
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%v %v %s", c.Chaos.ExcludedNamespaces, c.Chaos.ExcludedServices, c.Chaos.FailureImage) != "[tac-prod-quiet] [kernel] example.com/pause:1" {
		t.Errorf("chaos = %+v", c.Chaos)
	}
	if _, err := Parse([]byte(`{chaos: {unknown: 1}}`)); err == nil {
		t.Error("an unknown chaos key was accepted")
	}
}

func TestReadersFor(t *testing.T) {
	c, err := Parse([]byte(`
readers:
  deployments:
    seatrials:
      - {kind: User, name: seatrials-deploy@example.iam.gserviceaccount.com}
      - {kind: Group, name: seatrials-oncall@example.com, apiGroup: rbac.authorization.k8s.io}
`))
	if err != nil {
		t.Fatal(err)
	}
	want := []rbacv1.Subject{
		{Kind: rbacv1.UserKind, APIGroup: rbacv1.GroupName, Name: "seatrials-deploy@example.iam.gserviceaccount.com"},
		{Kind: rbacv1.GroupKind, APIGroup: rbacv1.GroupName, Name: "seatrials-oncall@example.com"},
	}
	if got := c.ReadersFor("seatrials"); !reflect.DeepEqual(got, want) {
		t.Errorf("ReadersFor(seatrials) = %+v, want %+v", got, want)
	}
	if got := c.ReadersFor("prod"); len(got) != 0 {
		t.Errorf("ReadersFor(prod) = %+v, want none", got)
	}
}
