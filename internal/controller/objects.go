package controller

import (
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/tacplus-dev/deploymentproxy/internal/config"
)

// desiredObjects are the objects deploymentproxy keeps in tenant namespace child of deployment, asked for by
// TenantNamespace owner in namespace parent. No NetworkPolicy: isolation is the bearer token checked at every door,
// not reachability (tacplus-dev/roadmap decisions/s2s-never-crosses-tenants-2026-10-31.md).
func desiredObjects(owner types.NamespacedName, child, deployment, parent string, cfg *config.Config) []client.Object {
	om := func(name string) metav1.ObjectMeta {
		return metav1.ObjectMeta{
			Namespace:   child,
			Name:        name,
			Labels:      map[string]string{OwnerLabel: deployment},
			Annotations: map[string]string{OwnerAnnotation: owner.String()},
		}
	}
	return []client.Object{
		// The deployment's operator is admin of this namespace and nothing beyond it.
		&rbacv1.RoleBinding{
			ObjectMeta: om(RoleBindingName),
			RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: AdminClusterRole},
			Subjects:   []rbacv1.Subject{{Kind: rbacv1.ServiceAccountKind, Name: OperatorServiceAccount, Namespace: parent}},
		},
		// No load balancers or node ports, whatever the deployment's limits.
		&corev1.ResourceQuota{
			ObjectMeta: om(QuotaName),
			Spec:       corev1.ResourceQuotaSpec{Hard: cfg.QuotaFor(deployment)},
		},
	}
}

// retiredObjects are the objects earlier versions of deploymentproxy kept in tenant namespace child, removed on
// reconcile: the baseline NetworkPolicies.
func retiredObjects(child string) []client.Object {
	return []client.Object{
		&networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Namespace: child, Name: DenyPolicyName}},
		&networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Namespace: child, Name: AllowPolicyName}},
	}
}
