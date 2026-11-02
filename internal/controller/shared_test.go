package controller

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// reaches reports whether every pod of namespace from may open a connection to every pod of namespace to,
// on any port, under the NetworkPolicies of both: from's must allow the egress and to's the ingress. A
// namespace with no policy of a direction allows it. envtest enforces no policies, so this evaluates them as a
// network plugin would, for the policies deploymentproxy writes (every one selects every pod of its namespace).
func reaches(from, to string) (bool, error) {
	ctx := context.Background()
	fromNS, err := getNamespace(from)
	if err != nil {
		return false, err
	}
	toNS, err := getNamespace(to)
	if err != nil {
		return false, err
	}
	egress, err := allows(ctx, fromNS, toNS, networkingv1.PolicyTypeEgress)
	if err != nil || !egress {
		return false, err
	}
	return allows(ctx, toNS, fromNS, networkingv1.PolicyTypeIngress)
}

// allows reports whether the policies of namespace self allow traffic of direction dir with every pod of
// namespace peer, on any port.
func allows(ctx context.Context, self, peer *corev1.Namespace, dir networkingv1.PolicyType) (bool, error) {
	var policies networkingv1.NetworkPolicyList
	if err := admin.List(ctx, &policies, client.InNamespace(self.Name)); err != nil {
		return false, err
	}
	isolated := false
	for _, p := range policies.Items {
		if len(p.Spec.PodSelector.MatchLabels)+len(p.Spec.PodSelector.MatchExpressions) != 0 {
			return false, fmt.Errorf("%s/%s selects only some pods", self.Name, p.Name)
		}
		applies := false
		for _, t := range p.Spec.PolicyTypes {
			applies = applies || t == dir
		}
		if !applies {
			continue
		}
		isolated = true
		type rule struct {
			peers []networkingv1.NetworkPolicyPeer
			ports []networkingv1.NetworkPolicyPort
		}
		var rules []rule
		if dir == networkingv1.PolicyTypeEgress {
			for _, r := range p.Spec.Egress {
				rules = append(rules, rule{r.To, r.Ports})
			}
		} else {
			for _, r := range p.Spec.Ingress {
				rules = append(rules, rule{r.From, r.Ports})
			}
		}
		for _, r := range rules {
			if len(r.ports) != 0 {
				continue
			}
			for _, pp := range r.peers {
				ok, err := selectsAll(pp, self, peer)
				if err != nil || ok {
					return ok, err
				}
			}
		}
	}
	return !isolated, nil
}

// selectsAll reports whether policy peer pp, in a policy of namespace self, selects every pod of namespace
// peer.
func selectsAll(pp networkingv1.NetworkPolicyPeer, self, peer *corev1.Namespace) (bool, error) {
	if pp.IPBlock != nil {
		return false, nil
	}
	if pp.PodSelector != nil && len(pp.PodSelector.MatchLabels)+len(pp.PodSelector.MatchExpressions) != 0 {
		return false, nil
	}
	if pp.NamespaceSelector == nil {
		return pp.PodSelector != nil && self.Name == peer.Name, nil
	}
	sel, err := metav1.LabelSelectorAsSelector(pp.NamespaceSelector)
	if err != nil {
		return false, err
	}
	return sel.Matches(labels.Set(peer.Labels)), nil
}

// checkReach returns an error unless reaches(from, to) is want.
func checkReach(from, to string, want bool) error {
	got, err := reaches(from, to)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("%s reaches %s: %v, want %v", from, to, got, want)
	}
	return nil
}

// checkReaches returns the first error of checkReach for each from -> to: want.
func checkReaches(cases []reachCase) error {
	for _, c := range cases {
		if err := checkReach(c.from, c.to, c.want); err != nil {
			return err
		}
	}
	return nil
}

type reachCase struct {
	from, to string
	want     bool
}

func readyTenants(t *testing.T, parent string, tids ...string) {
	t.Helper()
	for _, tid := range tids {
		tn := newTenantNamespace(parent, "t-"+tid, tid)
		create(t, tn)
		waitReady(t, tn, metav1.ConditionTrue, ReasonReady)
	}
}

func TestSharedTenant(t *testing.T) {
	// restore (00000002) is shared; people (00000001) and ordinary tenants are not.
	deploymentNamespace(t, "tac-mu", "mu")
	deploymentNamespace(t, "tac-nu", "nu")
	deploymentNamespace(t, "outsider", "")
	readyTenants(t, "tac-mu", "00000001", "00000002", "aaaaaaaa", "bbbbbbbb")
	readyTenants(t, "tac-nu", "00000002", "aaaaaaaa")

	eventually(t, func() error {
		return checkReaches([]reachCase{
			// Every tenant of a deployment reaches its restore tenant, and still its deployment namespace.
			{"tac-mu-aaaaaaaa", "tac-mu-00000002", true},
			{"tac-mu-bbbbbbbb", "tac-mu-00000002", true},
			{"tac-mu-00000001", "tac-mu-00000002", true},
			{"tac-nu-aaaaaaaa", "tac-nu-00000002", true},
			{"tac-mu-aaaaaaaa", "tac-mu", true},
			// Nothing crosses deployments.
			{"tac-nu-aaaaaaaa", "tac-mu-00000002", false},
			{"tac-mu-aaaaaaaa", "tac-nu-00000002", false},
			{"tac-nu-00000002", "tac-mu-00000002", false},
			// The restore tenant accepts its deployment's tenants only.
			{"outsider", "tac-mu-00000002", false},
			{"tac-mu", "tac-mu-00000002", false},
			// A tenant that is not shared is reached by no other tenant, and restore reaches no tenant.
			{"tac-mu-aaaaaaaa", "tac-mu-bbbbbbbb", false},
			{"tac-mu-aaaaaaaa", "tac-mu-00000001", false},
			{"tac-mu-00000002", "tac-mu-aaaaaaaa", false},
			{"tac-mu-00000002", "tac-mu-00000001", false},
		})
	})

	// A namespace merely named like a deployment's restore tenant is not reached: it is chosen by deploymentproxy's
	// labels, and these are another deployment's.
	create(t, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name:   "tac-xi-00000002",
		Labels: map[string]string{OwnerLabel: "notxi", TidLabel: "00000002"},
	}})
	deploymentNamespace(t, "tac-xi", "xi")
	readyTenants(t, "tac-xi", "aaaaaaaa")
	consistently(t, func() error { return checkReach("tac-xi-aaaaaaaa", "tac-xi-00000002", false) })
}

func TestSharedTidsReread(t *testing.T) {
	deploymentNamespace(t, "tac-omicron", "omicron")
	readyTenants(t, "tac-omicron", "00000001", "00000002", "aaaaaaaa")
	eventually(t, func() error {
		return checkReaches([]reachCase{
			{"tac-omicron-aaaaaaaa", "tac-omicron-00000002", true},
			{"tac-omicron-aaaaaaaa", "tac-omicron-00000001", false},
		})
	})

	// Sharing people instead of restore reaches the existing namespaces without touching them.
	setConfig(t, `
reservedTids: ["00000001", "00000002"]
sharedTids: ["00000001"]
`)
	eventually(t, func() error {
		return checkReaches([]reachCase{
			{"tac-omicron-aaaaaaaa", "tac-omicron-00000002", false},
			{"tac-omicron-aaaaaaaa", "tac-omicron-00000001", true},
			{"tac-omicron-00000002", "tac-omicron-00000001", true},
		})
	})

	// Sharing nothing.
	setConfig(t, `reservedTids: ["00000001", "00000002"]`)
	eventually(t, func() error {
		return checkReaches([]reachCase{
			{"tac-omicron-aaaaaaaa", "tac-omicron-00000002", false},
			{"tac-omicron-aaaaaaaa", "tac-omicron-00000001", false},
			{"tac-omicron-aaaaaaaa", "tac-omicron", true},
		})
	})
}
