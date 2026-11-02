package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deploymentproxyv1 "github.com/tacplus-dev/deploymentproxy/api/v1"
)

func newTenantNamespace(namespace, name, tid string) *deploymentproxyv1.TenantNamespace {
	return &deploymentproxyv1.TenantNamespace{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec:       deploymentproxyv1.TenantNamespaceSpec{Tid: tid},
	}
}

func create(t *testing.T, obj client.Object) {
	t.Helper()
	if err := admin.Create(context.Background(), obj); err != nil {
		t.Fatal(err)
	}
}

// waitReady waits for tn's Ready condition to have status and reason, and returns tn as it is then.
func waitReady(t *testing.T, tn *deploymentproxyv1.TenantNamespace, status metav1.ConditionStatus, reason string) *deploymentproxyv1.TenantNamespace {
	t.Helper()
	got := &deploymentproxyv1.TenantNamespace{}
	eventually(t, func() error {
		if err := admin.Get(context.Background(), client.ObjectKeyFromObject(tn), got); err != nil {
			return err
		}
		c := meta.FindStatusCondition(got.Status.Conditions, ReadyCondition)
		if c == nil || c.Status != status || c.Reason != reason || c.ObservedGeneration != got.Generation {
			return fmt.Errorf("%s: Ready is %+v, want %s/%s", tn.Name, c, status, reason)
		}
		return nil
	})
	return got
}

func getNamespace(name string) (*corev1.Namespace, error) {
	ns := &corev1.Namespace{}
	return ns, admin.Get(context.Background(), types.NamespacedName{Name: name}, ns)
}

// waitDeleting waits until namespace name is gone or being deleted. envtest runs no namespace controller,
// so a deleted namespace stays Terminating.
func waitDeleting(t *testing.T, name string) {
	t.Helper()
	eventually(t, func() error {
		ns, err := getNamespace(name)
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if ns.DeletionTimestamp == nil {
			return fmt.Errorf("namespace %s is not being deleted", name)
		}
		return nil
	})
}

// removeNamespace deletes namespace name completely, doing the namespace controller's part.
func removeNamespace(t *testing.T, name string) {
	t.Helper()
	ctx := context.Background()
	cs := kubernetes.NewForConfigOrDie(adminCfg)
	if err := cs.CoreV1().Namespaces().Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	ns, err := cs.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ns.Spec.Finalizers = nil
	if _, err := cs.CoreV1().Namespaces().Finalize(ctx, ns, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := cs.CoreV1().Namespaces().Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		t.Fatal(err)
	}
}

func TestTidValidatedByCRD(t *testing.T) {
	ctx := context.Background()
	deploymentNamespace(t, "tac-crd", "crd")
	for _, tid := range []string{"", "abc", "abcdefghi", "ABCDEFGH", "abcd-fgh", "abcd_fgh"} {
		err := admin.Create(ctx, newTenantNamespace("tac-crd", "bad", tid))
		if !apierrors.IsInvalid(err) {
			t.Errorf("tid %q: got %v, want Invalid", tid, err)
		}
	}

	tn := newTenantNamespace("tac-crd", "t", "a1b2c3d4")
	create(t, tn)
	tn.Spec.Tid = "a1b2c3d5"
	err := admin.Update(ctx, tn)
	if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), "tid is immutable") {
		t.Fatalf("changing tid: got %v, want Invalid: tid is immutable", err)
	}
}

func TestReservedTids(t *testing.T) {
	deploymentNamespace(t, "tac-rsv", "rsv")

	refused := newTenantNamespace("tac-rsv", "unreserved", "00000003")
	create(t, refused)
	waitReady(t, refused, metav1.ConditionFalse, ReasonReservedTid)
	if _, err := getNamespace("tac-rsv-00000003"); !apierrors.IsNotFound(err) {
		t.Fatalf("tac-rsv-00000003: got %v, want NotFound", err)
	}

	people := newTenantNamespace("tac-rsv", "people", "00000001")
	create(t, people)
	got := waitReady(t, people, metav1.ConditionTrue, ReasonReady)
	if got.Status.Namespace != "tac-rsv-00000001" {
		t.Fatalf("status.namespace = %q", got.Status.Namespace)
	}
}

func TestDeploymentFromLabelNotName(t *testing.T) {
	// The parent's name says nothing; the label says the deployment is "alpha".
	deploymentNamespace(t, "tac-x-y", "alpha")
	tn := newTenantNamespace("tac-x-y", "t", "abcdefgh")
	create(t, tn)
	got := waitReady(t, tn, metav1.ConditionTrue, ReasonReady)
	if got.Status.Namespace != "tac-alpha-abcdefgh" {
		t.Fatalf("status.namespace = %q, want tac-alpha-abcdefgh", got.Status.Namespace)
	}
	ns, err := getNamespace("tac-alpha-abcdefgh")
	if err != nil {
		t.Fatal(err)
	}
	if ns.Labels[OwnerLabel] != "alpha" || ns.Labels[TidLabel] != "abcdefgh" {
		t.Fatalf("child labels = %v", ns.Labels)
	}
	if _, err := getNamespace("tac-x-y-abcdefgh"); !apierrors.IsNotFound(err) {
		t.Fatalf("tac-x-y-abcdefgh: got %v, want NotFound", err)
	}
}

func TestRefusedParents(t *testing.T) {
	deploymentNamespace(t, "no-label", "")
	unlabelled := newTenantNamespace("no-label", "t", "abcdefgh")
	create(t, unlabelled)
	waitReady(t, unlabelled, metav1.ConditionFalse, ReasonNotDeploymentNamespace)

	deploymentNamespace(t, "tac-badname", "Bad_Name")
	badLabel := newTenantNamespace("tac-badname", "t", "abcdefgh")
	create(t, badLabel)
	waitReady(t, badLabel, metav1.ConditionFalse, ReasonInvalidDeployment)

	// An operator asking from inside one of its tenant namespaces is refused, even if that namespace also
	// carries a deployment label.
	deploymentNamespace(t, "tac-gamma", "gamma")
	parent := newTenantNamespace("tac-gamma", "t", "abcdefgh")
	create(t, parent)
	waitReady(t, parent, metav1.ConditionTrue, ReasonReady)
	child, err := getNamespace("tac-gamma-abcdefgh")
	if err != nil {
		t.Fatal(err)
	}
	child.Labels[DeploymentLabel] = "gamma"
	if err := admin.Update(context.Background(), child); err != nil {
		t.Fatal(err)
	}
	nested := newTenantNamespace("tac-gamma-abcdefgh", "t", "zyxwvuts")
	create(t, nested)
	waitReady(t, nested, metav1.ConditionFalse, ReasonInTenantNamespace)

	for _, name := range []string{"no-label-abcdefgh", "tac--abcdefgh", "tac-gamma-zyxwvuts"} {
		if _, err := getNamespace(name); !apierrors.IsNotFound(err) {
			t.Errorf("%s: got %v, want NotFound", name, err)
		}
	}
}

func TestExistingNamespaceNotAdopted(t *testing.T) {
	ctx := context.Background()
	deploymentNamespace(t, "tac-beta", "beta")
	deploymentNamespace(t, "tac-beta-abcdefgh", "") // someone else's, same name
	other := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name:   "tac-beta-zyxwvuts",
		Labels: map[string]string{OwnerLabel: "notbeta", TidLabel: "zyxwvuts"},
	}}
	create(t, other) // deploymentproxy's label, another deployment

	for _, tid := range []string{"abcdefgh", "zyxwvuts"} {
		tn := newTenantNamespace("tac-beta", "t-"+tid, tid)
		create(t, tn)
		waitReady(t, tn, metav1.ConditionFalse, ReasonNamespaceConflict)
	}
	for name, wantOwner := range map[string]string{"tac-beta-abcdefgh": "", "tac-beta-zyxwvuts": "notbeta"} {
		consistently(t, func() error {
			ns, err := getNamespace(name)
			if err != nil {
				return err
			}
			if ns.Labels[OwnerLabel] != wantOwner {
				return fmt.Errorf("%s: owner label changed to %q", name, ns.Labels[OwnerLabel])
			}
			var rbs rbacv1.RoleBindingList
			if err := admin.List(ctx, &rbs, client.InNamespace(name)); err != nil {
				return err
			}
			if len(rbs.Items) != 0 {
				return fmt.Errorf("%s: deploymentproxy wrote RoleBindings into it", name)
			}
			return nil
		})
	}

	// Deleting a refused TenantNamespace leaves the namespace it was refused alone.
	if err := admin.Delete(ctx, newTenantNamespace("tac-beta", "t-abcdefgh", "abcdefgh")); err != nil {
		t.Fatal(err)
	}
	consistently(t, func() error {
		ns, err := getNamespace("tac-beta-abcdefgh")
		if err != nil {
			return err
		}
		if ns.DeletionTimestamp != nil {
			return fmt.Errorf("tac-beta-abcdefgh is being deleted")
		}
		return nil
	})
}

func TestSameTidTwiceRefused(t *testing.T) {
	deploymentNamespace(t, "tac-twice", "twice")
	first := newTenantNamespace("tac-twice", "first", "abcdefgh")
	create(t, first)
	waitReady(t, first, metav1.ConditionTrue, ReasonReady)
	second := newTenantNamespace("tac-twice", "second", "abcdefgh")
	create(t, second)
	waitReady(t, second, metav1.ConditionFalse, ReasonNamespaceConflict)

	if err := admin.Delete(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	consistently(t, func() error {
		ns, err := getNamespace("tac-twice-abcdefgh")
		if err != nil {
			return err
		}
		if ns.DeletionTimestamp != nil {
			return fmt.Errorf("deleting the refused duplicate deleted the first one's namespace")
		}
		return nil
	})
}

// checkDefaults returns an error unless namespace child of deployment (asked for from parent) has every
// default deploymentproxy gives it.
func checkDefaults(child, parent, deployment, pods string) error {
	ctx := context.Background()
	ns, err := getNamespace(child)
	if err != nil {
		return err
	}
	if ns.DeletionTimestamp != nil {
		return fmt.Errorf("%s is being deleted", child)
	}
	if ns.Labels[OwnerLabel] != deployment || ns.Labels[TidLabel] != child[len(child)-8:] {
		return fmt.Errorf("%s: labels %v", child, ns.Labels)
	}

	rb := &rbacv1.RoleBinding{}
	if err := admin.Get(ctx, types.NamespacedName{Namespace: child, Name: RoleBindingName}, rb); err != nil {
		return err
	}
	wantSubjects := []rbacv1.Subject{{Kind: rbacv1.ServiceAccountKind, Name: "operator", Namespace: parent}}
	if rb.RoleRef != (rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "admin"}) ||
		fmt.Sprint(rb.Subjects) != fmt.Sprint(wantSubjects) {
		return fmt.Errorf("RoleBinding: roleRef %+v subjects %+v", rb.RoleRef, rb.Subjects)
	}

	deny := &networkingv1.NetworkPolicy{}
	if err := admin.Get(ctx, types.NamespacedName{Namespace: child, Name: DenyPolicyName}, deny); err != nil {
		return err
	}
	if len(deny.Spec.PodSelector.MatchLabels)+len(deny.Spec.PodSelector.MatchExpressions) != 0 ||
		len(deny.Spec.PolicyTypes) != 2 || len(deny.Spec.Ingress)+len(deny.Spec.Egress) != 0 {
		return fmt.Errorf("default-deny policy: %+v", deny.Spec)
	}
	allow := &networkingv1.NetworkPolicy{}
	if err := admin.Get(ctx, types.NamespacedName{Namespace: child, Name: AllowPolicyName}, allow); err != nil {
		return err
	}
	reachesParent := false
	for _, e := range allow.Spec.Egress {
		for _, p := range e.To {
			if p.NamespaceSelector != nil && p.PodSelector == nil &&
				p.NamespaceSelector.MatchLabels[corev1.LabelMetadataName] == parent && len(e.Ports) == 0 {
				reachesParent = true
			}
		}
	}
	if !reachesParent {
		return fmt.Errorf("allow policy does not let %s reach %s: %+v", child, parent, allow.Spec)
	}

	q := &corev1.ResourceQuota{}
	if err := admin.Get(ctx, types.NamespacedName{Namespace: child, Name: QuotaName}, q); err != nil {
		return err
	}
	for name, want := range map[corev1.ResourceName]string{
		corev1.ResourceServicesLoadBalancers: "0",
		corev1.ResourceServicesNodePorts:     "0",
		corev1.ResourcePods:                  pods,
	} {
		got, ok := q.Spec.Hard[name]
		if !ok || got.Cmp(resource.MustParse(want)) != 0 {
			return fmt.Errorf("quota %s = %s, want %s", name, got.String(), want)
		}
	}
	return nil
}

func TestDefaults(t *testing.T) {
	deploymentNamespace(t, "tac-delta", "delta")
	tn := newTenantNamespace("tac-delta", "t", "abcdefgh")
	create(t, tn)
	waitReady(t, tn, metav1.ConditionTrue, ReasonReady)
	eventually(t, func() error { return checkDefaults("tac-delta-abcdefgh", "tac-delta", "delta", "10") })

	// A deployment's quota override applies; the ban on load balancers and node ports still does.
	deploymentNamespace(t, "tac-big", "big")
	big := newTenantNamespace("tac-big", "t", "abcdefgh")
	create(t, big)
	waitReady(t, big, metav1.ConditionTrue, ReasonReady)
	eventually(t, func() error { return checkDefaults("tac-big-abcdefgh", "tac-big", "big", "100") })
}

func TestPutBack(t *testing.T) {
	ctx := context.Background()
	deploymentNamespace(t, "tac-eps", "eps")
	tn := newTenantNamespace("tac-eps", "t", "abcdefgh")
	create(t, tn)
	waitReady(t, tn, metav1.ConditionTrue, ReasonReady)
	child := "tac-eps-abcdefgh"
	eventually(t, func() error { return checkDefaults(child, "tac-eps", "eps", "10") })

	// Objects inside it, changed or deleted by hand.
	if err := admin.Delete(ctx, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: child, Name: RoleBindingName}}); err != nil {
		t.Fatal(err)
	}
	if err := admin.Delete(ctx, &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Namespace: child, Name: DenyPolicyName}}); err != nil {
		t.Fatal(err)
	}
	q := &corev1.ResourceQuota{}
	if err := admin.Get(ctx, types.NamespacedName{Namespace: child, Name: QuotaName}, q); err != nil {
		t.Fatal(err)
	}
	q.Spec.Hard[corev1.ResourceServicesLoadBalancers] = resource.MustParse("5")
	q.Labels = nil // even unlabelled, it is still deploymentproxy's to put back
	if err := admin.Update(ctx, q); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() error { return checkDefaults(child, "tac-eps", "eps", "10") })

	// The namespace's tid label changed by hand.
	ns, err := getNamespace(child)
	if err != nil {
		t.Fatal(err)
	}
	ns.Labels[TidLabel] = "changed0"
	if err := admin.Update(ctx, ns); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() error { return checkDefaults(child, "tac-eps", "eps", "10") })

	// The namespace deleted by hand.
	oldUID := ns.UID
	removeNamespace(t, child)
	eventually(t, func() error {
		ns, err := getNamespace(child)
		if err != nil {
			return err
		}
		if ns.UID == oldUID {
			return fmt.Errorf("%s not recreated yet", child)
		}
		return checkDefaults(child, "tac-eps", "eps", "10")
	})
}

func TestDeletingObjectDeletesChild(t *testing.T) {
	deploymentNamespace(t, "tac-zeta", "zeta")
	tn := newTenantNamespace("tac-zeta", "t", "abcdefgh")
	keep := newTenantNamespace("tac-zeta", "keep", "zyxwvuts")
	create(t, tn)
	create(t, keep)
	waitReady(t, tn, metav1.ConditionTrue, ReasonReady)
	waitReady(t, keep, metav1.ConditionTrue, ReasonReady)

	if err := admin.Delete(context.Background(), tn); err != nil {
		t.Fatal(err)
	}
	waitDeleting(t, "tac-zeta-abcdefgh")
	consistently(t, func() error { return checkDefaults("tac-zeta-zyxwvuts", "tac-zeta", "zeta", "10") })
}

func TestDeletingParentDeletesChildren(t *testing.T) {
	ctx := context.Background()
	deploymentNamespace(t, "tac-eta", "eta")
	deploymentNamespace(t, "tac-theta", "theta")
	var tns []*deploymentproxyv1.TenantNamespace
	for _, tid := range []string{"aaaaaaaa", "bbbbbbbb", "cccccccc"} {
		tn := newTenantNamespace("tac-eta", "t-"+tid, tid)
		create(t, tn)
		tns = append(tns, tn)
	}
	other := newTenantNamespace("tac-theta", "t", "aaaaaaaa")
	create(t, other)
	for _, tn := range append(tns, other) {
		waitReady(t, tn, metav1.ConditionTrue, ReasonReady)
	}

	if err := admin.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tac-eta"}}); err != nil {
		t.Fatal(err)
	}
	for _, tid := range []string{"aaaaaaaa", "bbbbbbbb", "cccccccc"} {
		waitDeleting(t, "tac-eta-"+tid)
	}
	consistently(t, func() error { return checkDefaults("tac-theta-aaaaaaaa", "tac-theta", "theta", "10") })
}

func TestDeletingParentAfterItsObjects(t *testing.T) {
	// The namespace controller deletes a namespace's TenantNamespaces before the namespace itself; if
	// deploymentproxy missed those deletions (it was down), the children are still found and deleted.
	deploymentNamespace(t, "tac-iota", "iota")
	tn := newTenantNamespace("tac-iota", "t", "abcdefgh")
	create(t, tn)
	waitReady(t, tn, metav1.ConditionTrue, ReasonReady)
	removeNamespace(t, "tac-iota")
	waitDeleting(t, "tac-iota-abcdefgh")
}

func TestOperatorRBAC(t *testing.T) {
	ctx := context.Background()
	deploymentNamespace(t, "tac-kappa", "kappa")
	if err := applyFile(ctx, admin, "../../config/deployment-namespace/operator.yaml", map[string]string{"${NAMESPACE}": "tac-kappa"}); err != nil {
		t.Fatal(err)
	}
	operator, err := client.New(impersonate(adminCfg, "tac-kappa", "operator"), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	other, err := client.New(impersonate(adminCfg, "tac-kappa", "gateway"), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}

	tn := newTenantNamespace("tac-kappa", "t", "abcdefgh")
	if err := operator.Create(ctx, tn); err != nil {
		t.Fatalf("operator creating in its own namespace: %v", err)
	}
	got := waitReady(t, tn, metav1.ConditionTrue, ReasonReady)

	// Only deploymentproxy writes status.
	got.Status.Namespace = "kube-system"
	if err := operator.Status().Update(ctx, got); !apierrors.IsForbidden(err) {
		t.Errorf("operator writing status: got %v, want Forbidden", err)
	}
	// Another service account of the deployment may not ask.
	if err := other.Create(ctx, newTenantNamespace("tac-kappa", "u", "zyxwvuts")); !apierrors.IsForbidden(err) {
		t.Errorf("another service account creating: got %v, want Forbidden", err)
	}
	// The operator cannot ask from inside its tenant namespace, nor from another deployment's.
	deploymentNamespace(t, "tac-lambda", "lambda")
	for _, ns := range []string{"tac-kappa-abcdefgh", "tac-lambda"} {
		if err := operator.Create(ctx, newTenantNamespace(ns, "t", "zyxwvuts")); !apierrors.IsForbidden(err) {
			t.Errorf("operator creating in %s: got %v, want Forbidden", ns, err)
		}
	}

	if err := operator.Delete(ctx, tn); err != nil {
		t.Fatalf("operator deleting: %v", err)
	}
	waitDeleting(t, "tac-kappa-abcdefgh")
}
