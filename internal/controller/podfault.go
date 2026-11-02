package controller

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deploymentproxyv1 "github.com/tacplus-dev/deploymentproxy/api/v1"
	"github.com/tacplus-dev/deploymentproxy/internal/config"
)

const (
	// PodFaultLabel marks a pod a PodFault of action Failure holds, with the PodFault's UID, so that no other
	// fault takes it meanwhile. deploymentproxy never trusts it otherwise: what it reverts, and how, is in the
	// PodFault's status.
	PodFaultLabel = "deploymentproxy.tacplus.dev/pod-fault"
	// PodFaultAnnotation names the PodFault (<namespace>/<name>) holding the pod, for people.
	PodFaultAnnotation = "deploymentproxy.tacplus.dev/pod-fault"
	// RevertFinalizer holds a PodFault of action Failure until its pod is put back.
	RevertFinalizer = "deploymentproxy.tacplus.dev/revert"
	// ServiceLabel names a pod's service; the exclusion list names services by it.
	ServiceLabel = "app.kubernetes.io/name"

	AppliedCondition  = "Applied"
	RevertedCondition = "Reverted"

	ReasonApplied             = "Applied"
	ReasonApplying            = "Applying"
	ReasonActive              = "Active"
	ReasonExpired             = "Expired"
	ReasonTargetGone          = "TargetGone"
	ReasonOtherDeployment     = "OtherDeployment"
	ReasonUnlabelledNamespace = "UnlabelledNamespace"
	ReasonExcludedNamespace   = "ExcludedNamespace"
	ReasonExcludedPod         = "ExcludedPod"
	ReasonSecretMaterial      = "SecretMaterial"
	ReasonTargetNotFound      = "TargetNotFound"
	ReasonTargetBusy          = "TargetBusy"
	ReasonOutOfBounds         = "OutOfBounds"
	ReasonInvalidSpec         = "InvalidSpec"

	// The hard bounds of a PodFault.
	MinFaultDuration      = time.Second
	MaxFaultDuration      = 5 * time.Minute
	MaxGracePeriodSeconds = 30
)

var (
	// excludedNamespaces are never targeted, beyond every kube-* one and those configured; a namespace outside
	// the requesting deployment is refused anyway, these hold even if one is mislabelled.
	excludedNamespaces = []string{"default", "deploymentproxy", "letsencrypt", "cert-manager"}
	// excludedServices are never targeted, beyond those configured: certificate issuance and the chaos
	// controller that asks.
	excludedServices = []string{"letsencrypt", "cert-manager", "chaos-controller"}
)

// PodFaultReconciler reconciles PodFault objects: it applies each fault once, if the requesting deployment may
// have it, and reverts it at its end or when the object is deleted.
type PodFaultReconciler struct {
	Client client.Client
	// APIReader reads PodFaults and pods uncached: a fault is applied once, so each reconcile must see the status
	// the last one wrote; and pods are not cached at all.
	APIReader client.Reader
	// Config returns the current configuration; it is called on every reconcile.
	Config func() (*config.Config, error)
	// Now is the clock; time.Now if nil.
	Now func() time.Time
}

// SetupWithManager registers the controller.
func (r *PodFaultReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("podfault").
		For(&deploymentproxyv1.PodFault{}).
		Complete(r)
}

func (r *PodFaultReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Reconcile applies a PodFault, reverts it at its end, or reverts it when it is deleted.
func (r *PodFaultReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	pf := &deploymentproxyv1.PodFault{}
	if err := r.APIReader.Get(ctx, req.NamespacedName, pf); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	if pf.DeletionTimestamp != nil {
		return reconcile.Result{}, r.finalize(ctx, pf)
	}

	before := pf.DeepCopy()
	pf.Status.Tag = pf.Spec.Tag
	applied := meta.FindStatusCondition(pf.Status.Conditions, AppliedCondition)
	var result reconcile.Result
	var err error
	switch {
	case applied == nil || applied.Status != metav1.ConditionTrue && (applied.Reason == ReasonError || applied.Reason == ReasonApplying):
		result, err = r.apply(ctx, before, pf)
	case applied.Status != metav1.ConditionTrue:
		// Refused: the spec is immutable, so it stays refused.
	case meta.IsStatusConditionTrue(pf.Status.Conditions, RevertedCondition):
	default:
		result, err = r.revertAtEnd(ctx, pf)
	}
	if serr := r.writeStatus(ctx, before, pf); serr != nil {
		return reconcile.Result{}, errors.Join(err, serr)
	}
	return result, err
}

// apply checks pf and applies it, or records why not. The pod is chosen, and recorded in status (with the images
// a Failure replaces), before anything is done to it; a retry acts on that pod only, so a fault never takes a
// second pod. before is kept equal to the status last written.
func (r *PodFaultReconciler) apply(ctx context.Context, before, pf *deploymentproxyv1.PodFault) (reconcile.Result, error) {
	err := r.applyOnce(ctx, before, pf)
	var refused *refusal
	switch {
	case errors.As(err, &refused):
		setCondition(pf, AppliedCondition, metav1.ConditionFalse, refused.reason, refused.message)
		return reconcile.Result{}, nil
	case err != nil:
		setCondition(pf, AppliedCondition, metav1.ConditionFalse, ReasonError, err.Error())
		return reconcile.Result{}, err
	}
	start := metav1.NewTime(r.now().Truncate(time.Second))
	end := metav1.NewTime(start.Add(mustDuration(pf.Spec.Duration)))
	pf.Status.StartTime, pf.Status.EndTime = &start, &end
	setCondition(pf, AppliedCondition, metav1.ConditionTrue, ReasonApplied, fmt.Sprintf("%s of pod %s/%s", pf.Spec.Action, pf.Status.Namespace, pf.Status.Pod))
	setCondition(pf, RevertedCondition, metav1.ConditionFalse, ReasonActive, "reverted at "+end.UTC().Format(time.RFC3339))
	log.FromContext(ctx).Info("applied pod fault", "action", pf.Spec.Action, "pod", pf.Status.Namespace+"/"+pf.Status.Pod, "tag", pf.Spec.Tag)
	return reconcile.Result{RequeueAfter: end.Sub(r.now())}, nil
}

func (r *PodFaultReconciler) applyOnce(ctx context.Context, before, pf *deploymentproxyv1.PodFault) error {
	cfg, err := r.Config()
	if err != nil {
		return err
	}
	chosen := pf.Status.PodUID == ""
	if chosen {
		pod, err := r.target(ctx, cfg, pf)
		if err != nil {
			return err
		}
		if pf.Spec.Action == deploymentproxyv1.PodFailure && !controllerutil.ContainsFinalizer(pf, RevertFinalizer) {
			base := pf.DeepCopy()
			controllerutil.AddFinalizer(pf, RevertFinalizer)
			if err := r.Client.Patch(ctx, pf, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
				return err
			}
			*before = *pf.DeepCopy()
			pf.Status.Tag = pf.Spec.Tag
		}
		pf.Status.Namespace, pf.Status.Pod, pf.Status.PodUID = pod.Namespace, pod.Name, pod.UID
		if pf.Spec.Action == deploymentproxyv1.PodFailure {
			pf.Status.OriginalImages = imagesOf(pod)
		}
		setCondition(pf, AppliedCondition, metav1.ConditionUnknown, ReasonApplying, fmt.Sprintf("%s of pod %s/%s", pf.Spec.Action, pod.Namespace, pod.Name))
		if err := r.writeStatus(ctx, before, pf); err != nil {
			return err
		}
		*before = *pf.DeepCopy()
	}

	if !chosen {
		if err := r.checkScope(ctx, cfg, pf, pf.Status.Namespace); err != nil {
			return err
		}
	}
	gone := &refusal{ReasonTargetNotFound, "pod " + pf.Status.Namespace + "/" + pf.Status.Pod + " went away before the fault was applied"}
	switch pf.Spec.Action {
	case deploymentproxyv1.PodKill:
		grace := int64(0)
		if pf.Spec.GracePeriodSeconds != nil {
			grace = *pf.Spec.GracePeriodSeconds
		}
		p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: pf.Status.Namespace, Name: pf.Status.Pod}}
		err := r.Client.Delete(ctx, p, client.Preconditions{UID: &pf.Status.PodUID}, client.GracePeriodSeconds(grace))
		if apierrors.IsNotFound(err) || apierrors.IsConflict(err) {
			if chosen {
				return gone
			}
			return nil // the pod is gone: a retry after the delete itself succeeded
		}
		return err
	case deploymentproxyv1.PodFailure:
		p := &corev1.Pod{}
		err := r.APIReader.Get(ctx, types.NamespacedName{Namespace: pf.Status.Namespace, Name: pf.Status.Pod}, p)
		if apierrors.IsNotFound(err) || err == nil && p.UID != pf.Status.PodUID {
			return gone
		}
		if err != nil {
			return err
		}
		if p.Labels[PodFaultLabel] == string(pf.UID) {
			return nil // applied before its status was written
		}
		if err := checkPod(cfg, pf, p); err != nil {
			return err
		}
		if images := imagesOf(p); !equality.Semantic.DeepEqual(images, pf.Status.OriginalImages) && !slices.Contains(slices.Collect(maps.Values(images)), cfg.Chaos.FailureImage) {
			// Changed since it was chosen: record what is put back before replacing it (never the failure
			// image itself, from a patch whose label was since removed).
			pf.Status.OriginalImages = images
			if err := r.writeStatus(ctx, before, pf); err != nil {
				return err
			}
			*before = *pf.DeepCopy()
		}
		base := p.DeepCopy()
		for i := range p.Spec.Containers {
			p.Spec.Containers[i].Image = cfg.Chaos.FailureImage
		}
		if p.Labels == nil {
			p.Labels = map[string]string{}
		}
		if p.Annotations == nil {
			p.Annotations = map[string]string{}
		}
		p.Labels[PodFaultLabel] = string(pf.UID)
		p.Annotations[PodFaultAnnotation] = client.ObjectKeyFromObject(pf).String()
		err = r.Client.Patch(ctx, p, client.StrategicMergeFrom(base, client.MergeFromWithOptimisticLock{}))
		if apierrors.IsNotFound(err) {
			return gone
		}
		return err
	}
	return fmt.Errorf("unknown action %q", pf.Spec.Action)
}

func imagesOf(p *corev1.Pod) map[string]string {
	images := map[string]string{}
	for _, c := range p.Spec.Containers {
		images[c.Name] = c.Image
	}
	return images
}

// mustDuration parses a duration checkSpec has accepted.
func mustDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		panic(err)
	}
	return d
}

// target checks pf's spec, its requester and its target, and returns the pod it is to act on.
func (r *PodFaultReconciler) target(ctx context.Context, cfg *config.Config, pf *deploymentproxyv1.PodFault) (*corev1.Pod, error) {
	if _, err := checkSpec(&pf.Spec); err != nil {
		return nil, err
	}
	namespace := pf.Spec.Target.Namespace
	if namespace == "" {
		namespace = pf.Namespace
	}
	if err := r.checkScope(ctx, cfg, pf, namespace); err != nil {
		return nil, err
	}
	return r.choosePod(ctx, cfg, pf, namespace)
}

// checkScope refuses namespace as a target of pf unless pf's own namespace is a deployment namespace and namespace
// is one of that deployment's. It is checked again before each action on the pod recorded in status.
func (r *PodFaultReconciler) checkScope(ctx context.Context, cfg *config.Config, pf *deploymentproxyv1.PodFault, namespace string) error {
	parent := &corev1.Namespace{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: pf.Namespace}, parent); err != nil {
		return err
	}
	deployment, err := deploymentOf(parent)
	if err != nil {
		return err
	}
	return r.checkNamespace(ctx, cfg, deployment, pf.Namespace, namespace)
}

// checkSpec checks pf's parameters against their bounds and returns its duration.
func checkSpec(spec *deploymentproxyv1.PodFaultSpec) (time.Duration, error) {
	d, err := time.ParseDuration(spec.Duration)
	if err != nil {
		return 0, &refusal{ReasonInvalidSpec, fmt.Sprintf("duration %q is not a duration (30s, 2m)", spec.Duration)}
	}
	if d < MinFaultDuration || d > MaxFaultDuration {
		return 0, &refusal{ReasonOutOfBounds, fmt.Sprintf("duration %s is not within %s to %s", d, MinFaultDuration, MaxFaultDuration)}
	}
	if g := spec.GracePeriodSeconds; g != nil {
		if spec.Action != deploymentproxyv1.PodKill {
			return 0, &refusal{ReasonInvalidSpec, "gracePeriodSeconds applies to Kill only"}
		}
		if *g < 0 || *g > MaxGracePeriodSeconds {
			return 0, &refusal{ReasonOutOfBounds, fmt.Sprintf("gracePeriodSeconds %d is not within 0 to %d", *g, MaxGracePeriodSeconds)}
		}
	}
	t := spec.Target
	if (t.Pod == "") == (t.Selector == nil) {
		return 0, &refusal{ReasonInvalidSpec, "target needs exactly one of pod and selector"}
	}
	if t.Selector != nil {
		if len(t.Selector.MatchLabels) == 0 && len(t.Selector.MatchExpressions) == 0 {
			return 0, &refusal{ReasonInvalidSpec, "target.selector is empty"}
		}
		if _, err := metav1.LabelSelectorAsSelector(t.Selector); err != nil {
			return 0, &refusal{ReasonInvalidSpec, "target.selector: " + err.Error()}
		}
	}
	return d, nil
}

// checkNamespace refuses a target namespace that is excluded or is not the requesting deployment's: its
// deployment namespace (where the request is) or one of the tenant namespaces deploymentproxy made for it.
func (r *PodFaultReconciler) checkNamespace(ctx context.Context, cfg *config.Config, deployment, requester, name string) error {
	if strings.HasPrefix(name, "kube-") || slices.Contains(excludedNamespaces, name) || slices.Contains(cfg.Chaos.ExcludedNamespaces, name) {
		return &refusal{ReasonExcludedNamespace, "namespace " + name + " is excluded"}
	}
	ns := &corev1.Namespace{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: name}, ns); apierrors.IsNotFound(err) {
		return &refusal{ReasonTargetNotFound, "namespace " + name + " does not exist"}
	} else if err != nil {
		return err
	}
	if name == requester {
		return nil
	}
	if owner, ok := ns.Labels[OwnerLabel]; ok {
		if req, _ := ownerOf(ns); owner == deployment && req.Namespace == requester {
			return nil
		}
		return &refusal{ReasonOtherDeployment, "namespace " + name + " is not a tenant namespace of deployment " + deployment}
	}
	if d, ok := ns.Labels[DeploymentLabel]; ok {
		return &refusal{ReasonOtherDeployment, fmt.Sprintf("namespace %s is deployment %q's, not the requesting namespace %s", name, d, requester)}
	}
	return &refusal{ReasonUnlabelledNamespace, "namespace " + name + " has neither " + DeploymentLabel + " nor " + OwnerLabel}
}

// choosePod returns the pod pf targets in namespace: the one named, or one of those its selector matches that it
// may target.
func (r *PodFaultReconciler) choosePod(ctx context.Context, cfg *config.Config, pf *deploymentproxyv1.PodFault, namespace string) (*corev1.Pod, error) {
	var pods []corev1.Pod
	if name := pf.Spec.Target.Pod; name != "" {
		p := corev1.Pod{}
		if err := r.APIReader.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &p); apierrors.IsNotFound(err) {
			return nil, &refusal{ReasonTargetNotFound, "pod " + namespace + "/" + name + " does not exist"}
		} else if err != nil {
			return nil, err
		}
		pods = append(pods, p)
	} else {
		sel, err := metav1.LabelSelectorAsSelector(pf.Spec.Target.Selector)
		if err != nil {
			return nil, &refusal{ReasonInvalidSpec, err.Error()}
		}
		var list corev1.PodList
		if err := r.APIReader.List(ctx, &list, client.InNamespace(namespace), client.MatchingLabelsSelector{Selector: sel}); err != nil {
			return nil, err
		}
		pods = list.Items
		sort.Slice(pods, func(i, j int) bool { return pods[i].Name < pods[j].Name })
	}

	var eligible []*corev1.Pod
	var first error
	for i := range pods {
		p := &pods[i]
		err := checkPod(cfg, pf, p)
		if err == nil {
			eligible = append(eligible, p)
		} else if first == nil {
			first = err
		}
	}
	switch {
	case len(eligible) > 0:
		return eligible[rand.IntN(len(eligible))], nil
	case first != nil:
		return nil, first
	default:
		return nil, &refusal{ReasonTargetNotFound, "no pod in " + namespace + " matches target.selector"}
	}
}

// checkPod refuses a pod that is going, excluded, holds secret material, or is held by another fault.
func checkPod(cfg *config.Config, pf *deploymentproxyv1.PodFault, p *corev1.Pod) error {
	where := p.Namespace + "/" + p.Name
	if p.DeletionTimestamp != nil {
		return &refusal{ReasonTargetNotFound, "pod " + where + " is being deleted"}
	}
	if s := p.Labels[ServiceLabel]; s != "" && (slices.Contains(excludedServices, s) || slices.Contains(cfg.Chaos.ExcludedServices, s)) {
		return &refusal{ReasonExcludedPod, "pod " + where + " is service " + s + ", which is excluded"}
	}
	for _, v := range p.Spec.Volumes {
		if mountsSecret(v) {
			return &refusal{ReasonSecretMaterial, "pod " + where + " mounts secret material (volume " + v.Name + ")"}
		}
	}
	if other, ok := p.Labels[PodFaultLabel]; ok && other != string(pf.UID) {
		return &refusal{ReasonTargetBusy, "pod " + where + " is held by PodFault " + p.Annotations[PodFaultAnnotation]}
	}
	return nil
}

// mountsSecret reports whether a volume carries a Secret or a CSI driver's content (secret stores mount by CSI).
func mountsSecret(v corev1.Volume) bool {
	if v.Secret != nil || v.CSI != nil {
		return true
	}
	if v.Projected != nil {
		for _, s := range v.Projected.Sources {
			if s.Secret != nil {
				return true
			}
		}
	}
	return false
}

// revertAtEnd reverts an applied fault once its end time has come.
func (r *PodFaultReconciler) revertAtEnd(ctx context.Context, pf *deploymentproxyv1.PodFault) (reconcile.Result, error) {
	if pf.Status.EndTime == nil {
		return reconcile.Result{}, fmt.Errorf("applied PodFault %s has no endTime", pf.Name)
	}
	if left := pf.Status.EndTime.Sub(r.now()); left > 0 {
		return reconcile.Result{RequeueAfter: left}, nil
	}
	reason, message := ReasonExpired, "the fault ended"
	if pf.Spec.Action == deploymentproxyv1.PodFailure {
		put, err := r.putBack(ctx, pf)
		if err != nil {
			return reconcile.Result{}, err
		}
		if !put {
			reason, message = ReasonTargetGone, "pod "+pf.Status.Namespace+"/"+pf.Status.Pod+" is gone; nothing to put back"
		} else {
			message = "the pod's images are put back"
		}
	}
	setCondition(pf, RevertedCondition, metav1.ConditionTrue, reason, message)
	log.FromContext(ctx).Info("reverted pod fault", "action", pf.Spec.Action, "pod", pf.Status.Namespace+"/"+pf.Status.Pod, "reason", reason, "tag", pf.Spec.Tag)
	return reconcile.Result{}, nil
}

// finalize reverts a deleted PodFault's Failure, if it still holds its pod, and lets the object go.
func (r *PodFaultReconciler) finalize(ctx context.Context, pf *deploymentproxyv1.PodFault) error {
	if !controllerutil.ContainsFinalizer(pf, RevertFinalizer) {
		return nil
	}
	if !meta.IsStatusConditionTrue(pf.Status.Conditions, RevertedCondition) {
		if _, err := r.putBack(ctx, pf); err != nil {
			return err
		}
		log.FromContext(ctx).Info("reverted pod fault of a deleted PodFault", "pod", pf.Status.Namespace+"/"+pf.Status.Pod, "tag", pf.Spec.Tag)
	}
	base := pf.DeepCopy()
	controllerutil.RemoveFinalizer(pf, RevertFinalizer)
	return r.Client.Patch(ctx, pf, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}

// putBack restores the images a Failure replaced on the pod recorded in pf's status, and removes its label; it
// reports false if that pod is gone. It acts only on the pod with the recorded UID, with the recorded images, and
// leaves alone a pod another PodFault holds; it is idempotent.
func (r *PodFaultReconciler) putBack(ctx context.Context, pf *deploymentproxyv1.PodFault) (bool, error) {
	if pf.Status.PodUID == "" {
		return true, nil
	}
	cfg, err := r.Config()
	if err != nil {
		return false, err
	}
	var refused *refusal
	if err := r.checkScope(ctx, cfg, pf, pf.Status.Namespace); errors.As(err, &refused) {
		log.FromContext(ctx).Info("not reverting a pod out of scope", "pod", pf.Status.Namespace+"/"+pf.Status.Pod, "reason", refused.Error())
		return true, nil
	} else if err != nil {
		return false, err
	}
	p := &corev1.Pod{}
	err = r.APIReader.Get(ctx, types.NamespacedName{Namespace: pf.Status.Namespace, Name: pf.Status.Pod}, p)
	if apierrors.IsNotFound(err) || err == nil && p.UID != pf.Status.PodUID {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	held := p.Labels[PodFaultLabel]
	if held != string(pf.UID) && (held != "" || !meta.IsStatusConditionTrue(pf.Status.Conditions, AppliedCondition)) {
		return true, nil // never applied here, or the pod is another fault's now
	}
	base := p.DeepCopy()
	for i := range p.Spec.Containers {
		if img, ok := pf.Status.OriginalImages[p.Spec.Containers[i].Name]; ok {
			p.Spec.Containers[i].Image = img
		}
	}
	delete(p.Labels, PodFaultLabel)
	delete(p.Annotations, PodFaultAnnotation)
	err = r.Client.Patch(ctx, p, client.StrategicMergeFrom(base, client.MergeFromWithOptimisticLock{}))
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

func setCondition(pf *deploymentproxyv1.PodFault, typ string, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&pf.Status.Conditions, metav1.Condition{
		Type:               typ,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: pf.Generation,
	})
}

// writeStatus records pf's status, if it changed.
func (r *PodFaultReconciler) writeStatus(ctx context.Context, before, pf *deploymentproxyv1.PodFault) error {
	if equality.Semantic.DeepEqual(before.Status, pf.Status) {
		return nil
	}
	return r.Client.Status().Patch(ctx, pf, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}
