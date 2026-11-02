package controller

import (
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/tacplus-dev/deploymentproxy/internal/config"
)

// desiredObjects are the objects deploymentproxy keeps in tenant namespace child of deployment, for tenant tid, asked
// for by TenantNamespace owner in namespace parent.
func desiredObjects(owner types.NamespacedName, child, deployment, tid, parent string, cfg *config.Config) []client.Object {
	om := func(name string) metav1.ObjectMeta {
		return metav1.ObjectMeta{
			Namespace:   child,
			Name:        name,
			Labels:      map[string]string{OwnerLabel: deployment},
			Annotations: map[string]string{OwnerAnnotation: owner.String()},
		}
	}
	everyPod := metav1.LabelSelector{}
	namespace := func(name string) *metav1.LabelSelector {
		return &metav1.LabelSelector{MatchLabels: map[string]string{corev1.LabelMetadataName: name}}
	}
	udp, tcp := corev1.ProtocolUDP, corev1.ProtocolTCP
	dns := intstr.FromInt32(53)

	ingress := []networkingv1.NetworkPolicyIngressRule{
		{From: []networkingv1.NetworkPolicyPeer{{PodSelector: &everyPod}}},
	}
	egress := []networkingv1.NetworkPolicyEgressRule{
		{To: []networkingv1.NetworkPolicyPeer{{PodSelector: &everyPod}}},
		{
			To: []networkingv1.NetworkPolicyPeer{{
				NamespaceSelector: namespace(metav1.NamespaceSystem),
				PodSelector:       &metav1.LabelSelector{MatchLabels: map[string]string{"k8s-app": "kube-dns"}},
			}},
			Ports: []networkingv1.NetworkPolicyPort{{Protocol: &udp, Port: &dns}, {Protocol: &tcp, Port: &dns}},
		},
		{To: []networkingv1.NetworkPolicyPeer{{NamespaceSelector: namespace(parent)}}},
	}
	// The deployment's shared tenants (restore), chosen by deploymentproxy's labels: every tenant namespace of the
	// deployment reaches each, and each accepts every tenant namespace of the deployment.
	for _, shared := range cfg.SharedTids {
		if shared == tid {
			ingress = append(ingress, networkingv1.NetworkPolicyIngressRule{
				From: []networkingv1.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{OwnerLabel: deployment},
				}}},
			})
			continue
		}
		egress = append(egress, networkingv1.NetworkPolicyEgressRule{
			To: []networkingv1.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{OwnerLabel: deployment, TidLabel: shared},
			}}},
		})
	}

	return []client.Object{
		// The deployment's operator is admin of this namespace and nothing beyond it.
		&rbacv1.RoleBinding{
			ObjectMeta: om(RoleBindingName),
			RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: AdminClusterRole},
			Subjects:   []rbacv1.Subject{{Kind: rbacv1.ServiceAccountKind, Name: OperatorServiceAccount, Namespace: parent}},
		},
		// Nothing in or out, unless a policy allows it.
		&networkingv1.NetworkPolicy{
			ObjectMeta: om(DenyPolicyName),
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: everyPod,
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
			},
		},
		// What is allowed: the namespace's own pods to each other, the cluster DNS (a name in the deployment
		// namespace must resolve), the deployment namespace, and the deployment's shared tenants. Everything
		// else is the operator's to add.
		&networkingv1.NetworkPolicy{
			ObjectMeta: om(AllowPolicyName),
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: everyPod,
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
				Ingress:     ingress,
				Egress:      egress,
			},
		},
		// No load balancers or node ports, whatever the deployment's limits.
		&corev1.ResourceQuota{
			ObjectMeta: om(QuotaName),
			Spec:       corev1.ResourceQuotaSpec{Hard: cfg.QuotaFor(deployment)},
		},
	}
}
