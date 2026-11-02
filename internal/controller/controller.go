// Package controller reconciles deploymentproxy's resources. TenantNamespace: for each one in a deployment
// namespace, the tenant namespace tac-<deployment>-<tid> exists with deploymentproxy's defaults; when the object
// or its namespace is gone, so is the tenant namespace. PodFault (podfault.go): a chaos fault on one pod of the
// requesting deployment, applied once and reverted at its end.
package controller

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deploymentproxyv1 "github.com/tacplus-dev/deploymentproxy/api/v1"
	"github.com/tacplus-dev/deploymentproxy/internal/config"
)

const (
	// DeploymentLabel marks a deployment namespace and names its deployment. The top pipeline sets it;
	// deploymentproxy only reads it.
	DeploymentLabel = "tacplus.dev/deployment"
	// OwnerLabel marks a tenant namespace as deploymentproxy's and names its deployment. deploymentproxy
	// changes or deletes a namespace only if it carries this label for the same deployment.
	OwnerLabel = "deploymentproxy.tacplus.dev/deployment"
	// TidLabel names a tenant namespace's tid.
	TidLabel = "deploymentproxy.tacplus.dev/tid"
	// OwnerAnnotation names the TenantNamespace (<namespace>/<name>) a tenant namespace, and each object
	// deploymentproxy keeps in it, is for.
	OwnerAnnotation = "deploymentproxy.tacplus.dev/owner"

	RoleBindingName = "deploymentproxy-operator-admin"
	DenyPolicyName  = "deploymentproxy-default-deny"
	AllowPolicyName = "deploymentproxy-allow"
	QuotaName       = "deploymentproxy-quota"

	// OperatorServiceAccount is the service account, in the deployment namespace, made admin of each of
	// its tenant namespaces.
	OperatorServiceAccount = "operator"
	// AdminClusterRole is the one ClusterRole deploymentproxy binds.
	AdminClusterRole = "admin"

	ReadyCondition = "Ready"

	ReasonReady                  = "Ready"
	ReasonNotDeploymentNamespace = "NotDeploymentNamespace"
	ReasonInTenantNamespace      = "InTenantNamespace"
	ReasonInvalidDeployment      = "InvalidDeployment"
	ReasonReservedTid            = "ReservedTid"
	ReasonNamespaceConflict      = "NamespaceConflict"
	ReasonPending                = "Pending"
	ReasonError                  = "Error"

	// ownerNamespaceIndex indexes tenant namespaces by the namespace of the TenantNamespace they are for.
	ownerNamespaceIndex = "deploymentproxy.ownerNamespace"
)

// deploymentPattern is a deployment name as the operator validates it: at most 16 characters of [a-z0-9-],
// starting with a letter.
var deploymentPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,15}$`)

// Reconciler reconciles TenantNamespace objects.
type Reconciler struct {
	Client client.Client
	// APIReader reads objects inside tenant namespaces uncached: the cache holds only those carrying
	// OwnerLabel, and one stripped of it by hand is still deploymentproxy's to put back.
	APIReader client.Reader
	// Config returns the current configuration; it is called on every reconcile.
	Config func() (*config.Config, error)
}

// CacheOptions limits the cluster-wide caches of the kinds deploymentproxy keeps inside tenant namespaces to
// deploymentproxy's own objects.
func CacheOptions() cache.Options {
	owned, err := labels.NewRequirement(OwnerLabel, selection.Exists, nil)
	if err != nil {
		panic(err)
	}
	sel := labels.NewSelector().Add(*owned)
	return cache.Options{ByObject: map[client.Object]cache.ByObject{
		&rbacv1.RoleBinding{}:         {Label: sel},
		&networkingv1.NetworkPolicy{}: {Label: sel},
		&corev1.ResourceQuota{}:       {Label: sel},
	}}
}

// SetupWithManager registers the controller and its watches.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	err := mgr.GetFieldIndexer().IndexField(context.Background(), &corev1.Namespace{}, ownerNamespaceIndex,
		func(o client.Object) []string {
			if req, ok := ownerOf(o); ok && o.GetLabels()[OwnerLabel] != "" {
				return []string{req.Namespace}
			}
			return nil
		})
	if err != nil {
		return err
	}
	toOwner := handler.EnqueueRequestsFromMapFunc(func(_ context.Context, o client.Object) []reconcile.Request {
		if req, ok := ownerOf(o); ok {
			return []reconcile.Request{{NamespacedName: req}}
		}
		return nil
	})
	return ctrl.NewControllerManagedBy(mgr).
		Named("tenantnamespace").
		For(&deploymentproxyv1.TenantNamespace{}).
		Watches(&corev1.Namespace{}, handler.EnqueueRequestsFromMapFunc(r.mapNamespace)).
		Watches(&rbacv1.RoleBinding{}, toOwner).
		Watches(&networkingv1.NetworkPolicy{}, toOwner).
		Watches(&corev1.ResourceQuota{}, toOwner).
		Complete(r)
}

// ownerOf returns the TenantNamespace an object of deploymentproxy's is for.
func ownerOf(o client.Object) (types.NamespacedName, bool) {
	ns, name, ok := strings.Cut(o.GetAnnotations()[OwnerAnnotation], "/")
	if !ok || ns == "" || name == "" {
		return types.NamespacedName{}, false
	}
	return types.NamespacedName{Namespace: ns, Name: name}, true
}

// mapNamespace maps a namespace event to the TenantNamespaces concerned: the one a tenant namespace is for,
// those living in it (its labels decide them), and those whose tenant namespaces it parents (still found
// after the TenantNamespaces themselves are gone).
func (r *Reconciler) mapNamespace(ctx context.Context, o client.Object) []reconcile.Request {
	seen := map[types.NamespacedName]bool{}
	if req, ok := ownerOf(o); ok {
		seen[req] = true
	}
	var tns deploymentproxyv1.TenantNamespaceList
	if err := r.Client.List(ctx, &tns, client.InNamespace(o.GetName())); err != nil {
		log.FromContext(ctx).Error(err, "listing TenantNamespaces", "namespace", o.GetName())
	}
	for _, tn := range tns.Items {
		seen[client.ObjectKeyFromObject(&tn)] = true
	}
	var children corev1.NamespaceList
	if err := r.Client.List(ctx, &children, client.MatchingFields{ownerNamespaceIndex: o.GetName()}); err != nil {
		log.FromContext(ctx).Error(err, "listing tenant namespaces", "parent", o.GetName())
	}
	for _, c := range children.Items {
		if req, ok := ownerOf(&c); ok {
			seen[req] = true
		}
	}
	reqs := make([]reconcile.Request, 0, len(seen))
	for req := range seen {
		reqs = append(reqs, reconcile.Request{NamespacedName: req})
	}
	return reqs
}

// refusal is a reason the TenantNamespace is not served; it is reported in status, not retried.
type refusal struct {
	reason, message string
}

func (e *refusal) Error() string { return e.reason + ": " + e.message }

// pending means the tenant namespace cannot be put in place yet; it is reported in status and retried.
type pending struct {
	message string
}

func (e *pending) Error() string { return e.message }

// Reconcile brings one TenantNamespace's tenant namespace to its desired state.
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	tn := &deploymentproxyv1.TenantNamespace{}
	if err := r.Client.Get(ctx, req.NamespacedName, tn); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, r.deleteChildren(ctx, req.NamespacedName, "")
		}
		return reconcile.Result{}, err
	}
	parent := &corev1.Namespace{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: tn.Namespace}, parent); err != nil && !apierrors.IsNotFound(err) {
		return reconcile.Result{}, err
	} else if err != nil || parent.DeletionTimestamp != nil || tn.DeletionTimestamp != nil {
		return reconcile.Result{}, r.deleteChildren(ctx, req.NamespacedName, "")
	}

	child, err := r.ensure(ctx, tn, parent)
	var refused *refusal
	var waiting *pending
	switch {
	case err == nil:
		return reconcile.Result{}, r.setStatus(ctx, tn, child, metav1.ConditionTrue, ReasonReady, "namespace "+child+" is in place")
	case errors.As(err, &refused):
		return reconcile.Result{}, r.setStatus(ctx, tn, "", metav1.ConditionFalse, refused.reason, refused.message)
	case errors.As(err, &waiting):
		return reconcile.Result{RequeueAfter: 2 * time.Second}, r.setStatus(ctx, tn, "", metav1.ConditionFalse, ReasonPending, waiting.message)
	default:
		if serr := r.setStatus(ctx, tn, "", metav1.ConditionFalse, ReasonError, err.Error()); serr != nil {
			log.FromContext(ctx).Error(serr, "writing status")
		}
		return reconcile.Result{}, err
	}
}

// ensure puts tn's tenant namespace, and everything deploymentproxy keeps in it, in place, and returns its name.
func (r *Reconciler) ensure(ctx context.Context, tn *deploymentproxyv1.TenantNamespace, parent *corev1.Namespace) (string, error) {
	cfg, err := r.Config()
	if err != nil {
		return "", err
	}
	deployment, err := deploymentOf(parent)
	if err != nil {
		return "", err
	}
	if !cfg.ValidTid(tn.Spec.Tid) {
		return "", &refusal{ReasonReservedTid, fmt.Sprintf("tid %q is in the reserved all-digit range and not configured", tn.Spec.Tid)}
	}

	owner := client.ObjectKeyFromObject(tn)
	name := "tac-" + deployment + "-" + tn.Spec.Tid
	if err := r.ensureNamespace(ctx, owner, name, deployment, tn.Spec.Tid); err != nil {
		return "", err
	}
	for _, obj := range desiredObjects(owner, name, deployment, tn.Spec.Tid, parent.Name, cfg) {
		if err := r.ensureObject(ctx, obj); err != nil {
			return "", err
		}
	}
	// A tenant namespace this object had under another name (its parent's deployment label changed).
	return name, r.deleteChildren(ctx, owner, name)
}

// deploymentOf returns the deployment whose namespace parent is, from its label, or refuses a request made in it.
func deploymentOf(parent *corev1.Namespace) (string, error) {
	if _, ok := parent.Labels[OwnerLabel]; ok {
		return "", &refusal{ReasonInTenantNamespace, "namespace " + parent.Name + " is a tenant namespace; ask from the deployment namespace"}
	}
	deployment, ok := parent.Labels[DeploymentLabel]
	if !ok {
		return "", &refusal{ReasonNotDeploymentNamespace, "namespace " + parent.Name + " has no " + DeploymentLabel + " label"}
	}
	if !deploymentPattern.MatchString(deployment) {
		return "", &refusal{ReasonInvalidDeployment, fmt.Sprintf("deployment %q (label %s of %s) is not 1-16 characters of [a-z0-9-] starting with a letter", deployment, DeploymentLabel, parent.Name)}
	}
	return deployment, nil
}

// ensureNamespace creates the tenant namespace, or puts back deploymentproxy's labels and annotation on it.
func (r *Reconciler) ensureNamespace(ctx context.Context, owner types.NamespacedName, name, deployment, tid string) error {
	ns := &corev1.Namespace{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: name}, ns)
	if apierrors.IsNotFound(err) {
		ns = &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Labels:      map[string]string{OwnerLabel: deployment, TidLabel: tid},
			Annotations: map[string]string{OwnerAnnotation: owner.String()},
		}}
		if err := r.Client.Create(ctx, ns); err != nil {
			if apierrors.IsAlreadyExists(err) {
				return &pending{"namespace " + name + " appeared while creating it"}
			}
			return err
		}
		log.FromContext(ctx).Info("created tenant namespace", "namespace", name)
		return nil
	}
	if err != nil {
		return err
	}
	if ns.Labels[OwnerLabel] != deployment {
		return &refusal{ReasonNamespaceConflict, "namespace " + name + " exists and is not deploymentproxy's for deployment " + deployment}
	}
	if other, ok := ownerOf(ns); ok && other != owner {
		if err := r.Client.Get(ctx, other, &deploymentproxyv1.TenantNamespace{}); err == nil {
			return &refusal{ReasonNamespaceConflict, "namespace " + name + " is already TenantNamespace " + other.String() + "'s"}
		} else if !apierrors.IsNotFound(err) {
			return err
		}
		// Its TenantNamespace is gone, and with it, shortly, the namespace.
		return &pending{"namespace " + name + " belonged to deleted TenantNamespace " + other.String()}
	}
	if ns.DeletionTimestamp != nil {
		return &pending{"namespace " + name + " is being deleted; it is recreated once it is gone"}
	}
	if ns.Labels[TidLabel] == tid && ns.Annotations[OwnerAnnotation] == owner.String() {
		return nil
	}
	patch := client.MergeFrom(ns.DeepCopy())
	ns.Labels[TidLabel] = tid
	if ns.Annotations == nil {
		ns.Annotations = map[string]string{}
	}
	ns.Annotations[OwnerAnnotation] = owner.String()
	return r.Client.Patch(ctx, ns, patch)
}

// ensureObject creates obj, or puts back its labels, annotations and content.
func (r *Reconciler) ensureObject(ctx context.Context, want client.Object) error {
	got := want.DeepCopyObject().(client.Object)
	err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(want), got)
	if apierrors.IsNotFound(err) {
		return r.Client.Create(ctx, want)
	}
	if err != nil {
		return err
	}
	if rb, ok := got.(*rbacv1.RoleBinding); ok && rb.RoleRef != want.(*rbacv1.RoleBinding).RoleRef {
		// roleRef is immutable.
		if err := r.Client.Delete(ctx, got, client.Preconditions{UID: &rb.UID}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		return r.Client.Create(ctx, want)
	}
	if !copyContent(want, got) {
		return nil
	}
	return r.Client.Update(ctx, got)
}

// copyContent copies deploymentproxy's labels, annotation and content from want into got, and reports whether got
// changed.
func copyContent(want, got client.Object) bool {
	changed := false
	l := got.GetLabels()
	if l == nil {
		l = map[string]string{}
	}
	for k, v := range want.GetLabels() {
		if l[k] != v {
			l[k], changed = v, true
		}
	}
	got.SetLabels(l)
	a := got.GetAnnotations()
	if a == nil {
		a = map[string]string{}
	}
	for k, v := range want.GetAnnotations() {
		if a[k] != v {
			a[k], changed = v, true
		}
	}
	got.SetAnnotations(a)
	switch g := got.(type) {
	case *rbacv1.RoleBinding:
		w := want.(*rbacv1.RoleBinding)
		if !equality.Semantic.DeepEqual(g.Subjects, w.Subjects) {
			g.Subjects, changed = w.Subjects, true
		}
	case *networkingv1.NetworkPolicy:
		w := want.(*networkingv1.NetworkPolicy)
		if !equality.Semantic.DeepEqual(g.Spec, w.Spec) {
			g.Spec, changed = w.Spec, true
		}
	case *corev1.ResourceQuota:
		w := want.(*corev1.ResourceQuota)
		if !equality.Semantic.DeepEqual(g.Spec, w.Spec) {
			g.Spec, changed = w.Spec, true
		}
	}
	return changed
}

// deleteChildren deletes every tenant namespace that is for TenantNamespace owner, except one named keep.
func (r *Reconciler) deleteChildren(ctx context.Context, owner types.NamespacedName, keep string) error {
	var children corev1.NamespaceList
	if err := r.Client.List(ctx, &children, client.MatchingFields{ownerNamespaceIndex: owner.Namespace}); err != nil {
		return err
	}
	for i := range children.Items {
		ns := &children.Items[i]
		if o, _ := ownerOf(ns); o != owner || ns.Name == keep || ns.DeletionTimestamp != nil {
			continue
		}
		if ns.Labels[OwnerLabel] == "" {
			continue // ownerNamespaceIndex holds only namespaces with OwnerLabel; this is defensive.
		}
		err := r.Client.Delete(ctx, ns, client.Preconditions{UID: &ns.UID}, client.PropagationPolicy(metav1.DeletePropagationBackground))
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		log.FromContext(ctx).Info("deleted tenant namespace", "namespace", ns.Name, "owner", owner.String())
	}
	return nil
}

// setStatus records the Ready condition and the tenant namespace's name, if they changed.
func (r *Reconciler) setStatus(ctx context.Context, tn *deploymentproxyv1.TenantNamespace, child string, status metav1.ConditionStatus, reason, message string) error {
	before := tn.DeepCopy()
	tn.Status.Namespace = child
	meta.SetStatusCondition(&tn.Status.Conditions, metav1.Condition{
		Type:               ReadyCondition,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: tn.Generation,
	})
	if equality.Semantic.DeepEqual(before.Status, tn.Status) {
		return nil
	}
	return r.Client.Status().Patch(ctx, tn, client.MergeFrom(before))
}
