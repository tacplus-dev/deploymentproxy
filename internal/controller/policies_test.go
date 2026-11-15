package controller

import (
	"context"
	"fmt"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
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

func TestEarlierPoliciesRemoved(t *testing.T) {
	ctx := context.Background()
	deploymentNamespace(t, "tac-mu", "mu")
	readyTenants(t, "tac-mu", "00000002", "aaaaaaaa")

	// The baseline policies an earlier deploymentproxy kept (one stripped of its labels by hand), and one of the
	// operator's own.
	for _, child := range []string{"tac-mu-00000002", "tac-mu-aaaaaaaa"} {
		ours := map[string]string{OwnerLabel: "mu"}
		owner := map[string]string{OwnerAnnotation: "tac-mu/t-" + child[len(child)-8:]}
		create(t, policy(child, DenyPolicyName, ours, owner))
		create(t, policy(child, AllowPolicyName, nil, nil))
		create(t, policy(child, "operator-own", nil, nil))
	}

	for _, child := range []string{"tac-mu-00000002", "tac-mu-aaaaaaaa"} {
		eventually(t, func() error {
			var policies networkingv1.NetworkPolicyList
			if err := admin.List(ctx, &policies, client.InNamespace(child)); err != nil {
				return err
			}
			if len(policies.Items) != 1 || policies.Items[0].Name != "operator-own" {
				names := []string{}
				for _, p := range policies.Items {
					names = append(names, p.Name)
				}
				return fmt.Errorf("%s: policies %v, want only operator-own", child, names)
			}
			return nil
		})
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
