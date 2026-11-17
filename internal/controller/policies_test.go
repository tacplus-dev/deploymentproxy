package controller

import (
	"context"
	"fmt"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/tacplus-dev/deploymentproxy/internal/config"
)

// Isolation is the bearer token checked at every door, not reachability (tacplus-dev/roadmap
// decisions/s2s-never-crosses-tenants-2026-10-31.md, addendum 2026-11-07 (2); #14): deploymentproxy keeps no
// NetworkPolicy in a tenant namespace, shared tenant or not.
func TestRenderedObjectsHaveNoNetworkPolicy(t *testing.T) {
	cfg, err := config.Parse([]byte(testConfig))
	if err != nil {
		t.Fatal(err)
	}
	owner := types.NamespacedName{Namespace: "tac-mu", Name: "t"}
	for _, tid := range []string{"00000001", "00000002", "aaaaaaaa"} {
		for _, obj := range desiredObjects(owner, "tac-mu-"+tid, "mu", "tac-mu", cfg) {
			if _, ok := obj.(*networkingv1.NetworkPolicy); ok {
				t.Errorf("tid %s: NetworkPolicy %s rendered", tid, obj.GetName())
			}
		}
	}
}

// policy is a NetworkPolicy in namespace, as earlier versions of deploymentproxy wrote theirs.
func policy(namespace, name string, labels, annotations map[string]string) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, Labels: labels, Annotations: annotations},
		Spec: networkingv1.NetworkPolicySpec{
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
		},
	}
}

// TestNetworkPoliciesLeftAlone proves deploymentproxy no longer removes the baseline NetworkPolicies versions
// before v0.2.2 kept (#24: v0.2.2 and later removed them on reconcile, #14), nor any other: a NetworkPolicy in a
// tenant namespace is not its to touch, and its ClusterRole grants nothing on networkpolicies.
func TestNetworkPoliciesLeftAlone(t *testing.T) {
	ctx := context.Background()
	deploymentNamespace(t, "tac-mu", "mu")
	readyTenants(t, "tac-mu", "00000002", "aaaaaaaa")

	names := []string{"deploymentproxy-default-deny", "deploymentproxy-allow", "operator-own"}
	for _, child := range []string{"tac-mu-00000002", "tac-mu-aaaaaaaa"} {
		for _, name := range names {
			create(t, policy(child, name, map[string]string{OwnerLabel: "mu"}, nil))
		}
		// A reconcile of the tenant namespace, which puts the RoleBinding back.
		if err := admin.Delete(ctx, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: child, Name: RoleBindingName}}); err != nil {
			t.Fatal(err)
		}
		eventually(t, func() error { return checkDefaults(child, "tac-mu", "mu", "10") })
	}

	consistently(t, func() error {
		for _, child := range []string{"tac-mu-00000002", "tac-mu-aaaaaaaa"} {
			var policies networkingv1.NetworkPolicyList
			if err := admin.List(ctx, &policies, client.InNamespace(child)); err != nil {
				return err
			}
			if len(policies.Items) != len(names) {
				return fmt.Errorf("%s: %d policies, want %v", child, len(policies.Items), names)
			}
		}
		return nil
	})

	dp, err := client.New(impersonate(adminCfg, "deploymentproxy", "deploymentproxy"), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		obj := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Namespace: "tac-mu-00000002", Name: name}}
		if err := dp.Delete(ctx, obj); !apierrors.IsForbidden(err) {
			t.Errorf("deploymentproxy deleting NetworkPolicy %s: got %v, want Forbidden", name, err)
		}
	}
}

func readyTenants(t *testing.T, parent string, tids ...string) {
	t.Helper()
	for _, tid := range tids {
		tn := newTenantNamespace(parent, "t-"+tid, tid)
		create(t, tn)
		waitReady(t, tn, metav1.ConditionTrue, ReasonReady)
	}
}
