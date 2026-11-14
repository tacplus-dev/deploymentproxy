package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deploymentproxyv1 "github.com/tacplus-dev/deploymentproxy/api/v1"
	"github.com/tacplus-dev/deploymentproxy/internal/config"
)

func newPodFault(namespace, name string, action deploymentproxyv1.PodFaultAction, target deploymentproxyv1.PodTarget, duration string) *deploymentproxyv1.PodFault {
	return &deploymentproxyv1.PodFault{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: deploymentproxyv1.PodFaultSpec{
			Tag:      "fleet=" + namespace + ",round=7,fault=" + name,
			Action:   action,
			Target:   target,
			Duration: duration,
		},
	}
}

// newPod creates a pod in namespace with labels, two containers, and no node (envtest runs no scheduler or
// kubelet, so it stays Pending and is deleted at once).
func newPod(t *testing.T, namespace, name string, labels map[string]string, mutate ...func(*corev1.Pod)) *corev1.Pod {
	t.Helper()
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, Labels: labels},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "app", Image: "example.com/app:1"},
			{Name: "sidecar", Image: "example.com/sidecar:2"},
		}},
	}
	for _, m := range mutate {
		m(p)
	}
	create(t, p)
	return p
}

// waitCondition waits for pf's condition typ to have status and reason, and returns pf as it is then.
func waitCondition(t *testing.T, pf *deploymentproxyv1.PodFault, typ string, status metav1.ConditionStatus, reason string) *deploymentproxyv1.PodFault {
	t.Helper()
	got := &deploymentproxyv1.PodFault{}
	eventually(t, func() error {
		if err := admin.Get(context.Background(), client.ObjectKeyFromObject(pf), got); err != nil {
			return err
		}
		c := meta.FindStatusCondition(got.Status.Conditions, typ)
		if c == nil || c.Status != status || c.Reason != reason {
			return fmt.Errorf("%s: %s is %+v, want %s/%s", pf.Name, typ, c, status, reason)
		}
		return nil
	})
	return got
}

func getPod(namespace, name string) (*corev1.Pod, error) {
	p := &corev1.Pod{}
	return p, admin.Get(context.Background(), types.NamespacedName{Namespace: namespace, Name: name}, p)
}

// podUntouched fails the test unless the pod is still there, not being deleted, with its own images.
func podUntouched(t *testing.T, namespace, name string) {
	t.Helper()
	p, err := getPod(namespace, name)
	if err != nil {
		t.Fatalf("pod %s/%s: %v", namespace, name, err)
	}
	if p.DeletionTimestamp != nil {
		t.Fatalf("pod %s/%s is being deleted", namespace, name)
	}
	if p.Spec.Containers[0].Image != "example.com/app:1" || p.Spec.Containers[1].Image != "example.com/sidecar:2" {
		t.Fatalf("pod %s/%s images changed: %v", namespace, name, p.Spec.Containers)
	}
	if _, ok := p.Labels[PodFaultLabel]; ok {
		t.Fatalf("pod %s/%s has %s", namespace, name, PodFaultLabel)
	}
}

func waitPodGone(t *testing.T, namespace, name string) {
	t.Helper()
	eventually(t, func() error {
		p, err := getPod(namespace, name)
		if apierrors.IsNotFound(err) || err == nil && p.DeletionTimestamp != nil {
			return nil
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("pod %s/%s is still there", namespace, name)
	})
}

func TestPodKillOwnNamespace(t *testing.T) {
	deploymentNamespace(t, "tac-pk", "pk")
	newPod(t, "tac-pk", "web-0", map[string]string{"app": "web"})
	newPod(t, "tac-pk", "db-0", map[string]string{"app": "db"})

	pf := newPodFault("tac-pk", "kill", deploymentproxyv1.PodKill, deploymentproxyv1.PodTarget{Pod: "web-0"}, "2s")
	create(t, pf)
	got := waitCondition(t, pf, AppliedCondition, metav1.ConditionTrue, ReasonApplied)
	if got.Status.Tag != pf.Spec.Tag {
		t.Errorf("status.tag = %q, want spec.tag %q unchanged", got.Status.Tag, pf.Spec.Tag)
	}
	if got.Status.Namespace != "tac-pk" || got.Status.Pod != "web-0" || got.Status.StartTime == nil || got.Status.EndTime == nil {
		t.Errorf("status = %+v", got.Status)
	}
	if d := got.Status.EndTime.Sub(got.Status.StartTime.Time); d != 2*time.Second {
		t.Errorf("endTime - startTime = %v, want 2s", d)
	}
	waitPodGone(t, "tac-pk", "web-0")
	podUntouched(t, "tac-pk", "db-0")

	// At its end the fault is reverted: a kill has nothing to put back.
	got = waitCondition(t, pf, RevertedCondition, metav1.ConditionTrue, ReasonExpired)
	if got.Status.Tag != pf.Spec.Tag {
		t.Errorf("status.tag = %q after the revert", got.Status.Tag)
	}
}

func TestPodKillTenantNamespaceBySelector(t *testing.T) {
	deploymentNamespace(t, "tac-pks", "pks")
	tn := newTenantNamespace("tac-pks", "t", "abcdefgh")
	create(t, tn)
	waitReady(t, tn, metav1.ConditionTrue, ReasonReady)
	newPod(t, "tac-pks-abcdefgh", "web-0", map[string]string{"app": "web"})
	newPod(t, "tac-pks-abcdefgh", "db-0", map[string]string{"app": "db"})

	pf := newPodFault("tac-pks", "kill", deploymentproxyv1.PodKill, deploymentproxyv1.PodTarget{
		Namespace: "tac-pks-abcdefgh",
		Selector:  &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
	}, "1m")
	pf.Spec.GracePeriodSeconds = ptr.To[int64](30)
	create(t, pf)
	got := waitCondition(t, pf, AppliedCondition, metav1.ConditionTrue, ReasonApplied)
	if got.Status.Namespace != "tac-pks-abcdefgh" || got.Status.Pod != "web-0" {
		t.Errorf("status = %+v", got.Status)
	}
	waitPodGone(t, "tac-pks-abcdefgh", "web-0")
	podUntouched(t, "tac-pks-abcdefgh", "db-0")
}

func TestPodFaultRefusedTargets(t *testing.T) {
	setConfig(t, testConfig+"chaos: {excludedNamespaces: [tac-rf-quiet0000], excludedServices: [kernel]}\n")
	deploymentNamespace(t, "tac-rf", "rf")
	deploymentNamespace(t, "tac-rfother", "rfother")
	deploymentNamespace(t, "tac-rf-twin", "rf") // labelled for the same deployment, but not the requester's namespace
	deploymentNamespace(t, "rf-plain", "")
	deploymentNamespace(t, "letsencrypt", "")
	deploymentNamespace(t, "tac-rf-quiet0000", "rf")
	other := newTenantNamespace("tac-rfother", "t", "abcdefgh")
	create(t, other)
	waitReady(t, other, metav1.ConditionTrue, ReasonReady)

	for _, ns := range []string{"tac-rfother", "tac-rfother-abcdefgh", "tac-rf-twin", "rf-plain", "letsencrypt", "kube-system", "default", "tac-rf-quiet0000"} {
		newPod(t, ns, "victim", nil)
	}
	newPod(t, "tac-rf", "certs", map[string]string{ServiceLabel: "letsencrypt"})
	newPod(t, "tac-rf", "kernel-0", map[string]string{ServiceLabel: "kernel"})
	newPod(t, "tac-rf", "keys", nil, func(p *corev1.Pod) {
		p.Spec.Volumes = []corev1.Volume{{Name: "pki", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "pki"}}}}
	})
	newPod(t, "tac-rf", "projected", nil, func(p *corev1.Pod) {
		p.Spec.Volumes = []corev1.Volume{{Name: "p", VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{
			Sources: []corev1.VolumeProjection{{Secret: &corev1.SecretProjection{LocalObjectReference: corev1.LocalObjectReference{Name: "uniconfig"}}}},
		}}}}
	})

	for _, c := range []struct {
		name, namespace, pod, reason string
	}{
		{"other-deployment", "tac-rfother", "victim", ReasonOtherDeployment},
		{"other-tenant", "tac-rfother-abcdefgh", "victim", ReasonOtherDeployment},
		{"twin", "tac-rf-twin", "victim", ReasonOtherDeployment},
		{"unlabelled", "rf-plain", "victim", ReasonUnlabelledNamespace},
		{"letsencrypt-ns", "letsencrypt", "victim", ReasonExcludedNamespace},
		{"kube-system", "kube-system", "victim", ReasonExcludedNamespace},
		{"default", "default", "victim", ReasonExcludedNamespace},
		{"configured-ns", "tac-rf-quiet0000", "victim", ReasonExcludedNamespace},
		{"letsencrypt-svc", "tac-rf", "certs", ReasonExcludedPod},
		{"configured-svc", "tac-rf", "kernel-0", ReasonExcludedPod},
		{"secret", "tac-rf", "keys", ReasonSecretMaterial},
		{"projected-secret", "tac-rf", "projected", ReasonSecretMaterial},
		{"no-namespace", "tac-rf-nowhere", "victim", ReasonTargetNotFound},
		{"no-pod", "tac-rf", "nobody", ReasonTargetNotFound},
	} {
		t.Run(c.name, func(t *testing.T) {
			pf := newPodFault("tac-rf", c.name, deploymentproxyv1.PodKill, deploymentproxyv1.PodTarget{Namespace: c.namespace, Pod: c.pod}, "10s")
			create(t, pf)
			got := waitCondition(t, pf, AppliedCondition, metav1.ConditionFalse, c.reason)
			if got.Status.Tag != pf.Spec.Tag {
				t.Errorf("status.tag = %q, want %q", got.Status.Tag, pf.Spec.Tag)
			}
			if got.Status.StartTime != nil {
				t.Errorf("a refused fault has startTime %v", got.Status.StartTime)
			}
			if c.reason != ReasonTargetNotFound {
				podUntouched(t, c.namespace, c.pod)
			}
		})
	}

	// A selector never chooses a pod it may not target, and is refused when it matches only those.
	newPod(t, "tac-rf", "certs-1", map[string]string{ServiceLabel: "letsencrypt", "tier": "edge"})
	pf := newPodFault("tac-rf", "selector", deploymentproxyv1.PodKill, deploymentproxyv1.PodTarget{
		Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"tier": "edge"}},
	}, "10s")
	create(t, pf)
	waitCondition(t, pf, AppliedCondition, metav1.ConditionFalse, ReasonExcludedPod)
	podUntouched(t, "tac-rf", "certs-1")
	consistently(t, func() error {
		_, err := getPod("tac-rf", "certs-1")
		return err
	})
}

func TestPodFaultRefusedRequesters(t *testing.T) {
	deploymentNamespace(t, "rq-plain", "")
	deploymentNamespace(t, "tac-rq", "rq")
	tn := newTenantNamespace("tac-rq", "t", "abcdefgh")
	create(t, tn)
	waitReady(t, tn, metav1.ConditionTrue, ReasonReady)
	newPod(t, "rq-plain", "victim", nil)
	newPod(t, "tac-rq-abcdefgh", "victim", nil)

	for ns, reason := range map[string]string{"rq-plain": ReasonNotDeploymentNamespace, "tac-rq-abcdefgh": ReasonInTenantNamespace} {
		pf := newPodFault(ns, "kill", deploymentproxyv1.PodKill, deploymentproxyv1.PodTarget{Pod: "victim"}, "10s")
		create(t, pf)
		waitCondition(t, pf, AppliedCondition, metav1.ConditionFalse, reason)
		podUntouched(t, ns, "victim")
	}
}

func TestPodFaultOutOfBounds(t *testing.T) {
	deploymentNamespace(t, "tac-ob", "ob")
	newPod(t, "tac-ob", "victim", map[string]string{"app": "victim"})

	byName := deploymentproxyv1.PodTarget{Pod: "victim"}
	for _, c := range []struct {
		name     string
		action   deploymentproxyv1.PodFaultAction
		target   deploymentproxyv1.PodTarget
		duration string
		grace    *int64
		reason   string
	}{
		{"too-long", deploymentproxyv1.PodKill, byName, "5m1s", nil, ReasonOutOfBounds},
		{"too-short", deploymentproxyv1.PodFailure, byName, "999ms", nil, ReasonOutOfBounds},
		{"zero", deploymentproxyv1.PodKill, byName, "0s", nil, ReasonOutOfBounds},
		{"negative", deploymentproxyv1.PodKill, byName, "-10s", nil, ReasonOutOfBounds},
		{"grace-high", deploymentproxyv1.PodKill, byName, "10s", ptr.To[int64](31), ReasonOutOfBounds},
		{"grace-negative", deploymentproxyv1.PodKill, byName, "10s", ptr.To[int64](-1), ReasonOutOfBounds},
		{"not-a-duration", deploymentproxyv1.PodKill, byName, "soon", nil, ReasonInvalidSpec},
		{"grace-on-failure", deploymentproxyv1.PodFailure, byName, "10s", ptr.To[int64](0), ReasonInvalidSpec},
		{"no-target", deploymentproxyv1.PodKill, deploymentproxyv1.PodTarget{}, "10s", nil, ReasonInvalidSpec},
		{"both-targets", deploymentproxyv1.PodKill, deploymentproxyv1.PodTarget{Pod: "victim", Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "victim"}}}, "10s", nil, ReasonInvalidSpec},
		{"empty-selector", deploymentproxyv1.PodKill, deploymentproxyv1.PodTarget{Selector: &metav1.LabelSelector{}}, "10s", nil, ReasonInvalidSpec},
		{"bad-selector", deploymentproxyv1.PodKill, deploymentproxyv1.PodTarget{Selector: &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "app", Operator: "Near"}}}}, "10s", nil, ReasonInvalidSpec},
	} {
		t.Run(c.name, func(t *testing.T) {
			pf := newPodFault("tac-ob", c.name, c.action, c.target, c.duration)
			pf.Spec.GracePeriodSeconds = c.grace
			create(t, pf)
			waitCondition(t, pf, AppliedCondition, metav1.ConditionFalse, c.reason)
			podUntouched(t, "tac-ob", "victim")
		})
	}
}

func TestPodFaultSpecImmutable(t *testing.T) {
	ctx := context.Background()
	deploymentNamespace(t, "tac-im", "im")
	pf := newPodFault("tac-im", "kill", deploymentproxyv1.PodKill, deploymentproxyv1.PodTarget{Pod: "nobody"}, "10s")
	create(t, pf)
	// The controller writes the PodFault (its finalizer, its refusal) after pf was read: the change is a merge patch,
	// which carries no resourceVersion, so only the immutability rule can refuse it, not a Conflict.
	waitCondition(t, pf, AppliedCondition, metav1.ConditionFalse, ReasonTargetNotFound)
	patch := client.MergeFrom(pf.DeepCopy())
	pf.Spec.Target.Pod = "somebody"
	if err := admin.Patch(ctx, pf, patch); !apierrors.IsInvalid(err) {
		t.Fatalf("changing the spec: got %v, want Invalid", err)
	}
	if err := admin.Create(ctx, newPodFault("tac-im", "bad", "Freeze", deploymentproxyv1.PodTarget{Pod: "nobody"}, "10s")); !apierrors.IsInvalid(err) {
		t.Fatalf("action Freeze: got %v, want Invalid", err)
	}
}

// failed reports whether every container of the pod runs the failure image, marked as PodFault pf's.
func failed(p *corev1.Pod, pf *deploymentproxyv1.PodFault) error {
	for _, c := range p.Spec.Containers {
		if c.Image != config.DefaultFailureImage {
			return fmt.Errorf("container %s has image %s", c.Name, c.Image)
		}
	}
	if p.Labels[PodFaultLabel] != string(pf.UID) {
		return fmt.Errorf("pod %s has %s=%q, want %s", p.Name, PodFaultLabel, p.Labels[PodFaultLabel], pf.UID)
	}
	return nil
}

func TestPodFailureRevertedAtEnd(t *testing.T) {
	deploymentNamespace(t, "tac-pf", "pf")
	newPod(t, "tac-pf", "web-0", map[string]string{"app": "web"})

	pf := newPodFault("tac-pf", "fail", deploymentproxyv1.PodFailure, deploymentproxyv1.PodTarget{Pod: "web-0"}, "3s")
	create(t, pf)
	got := waitCondition(t, pf, AppliedCondition, metav1.ConditionTrue, ReasonApplied)
	p, err := getPod("tac-pf", "web-0")
	if err != nil {
		t.Fatal(err)
	}
	if err := failed(p, got); err != nil {
		t.Fatal(err)
	}

	// A second fault on a pod already failed is refused.
	busy := newPodFault("tac-pf", "busy", deploymentproxyv1.PodFailure, deploymentproxyv1.PodTarget{Pod: "web-0"}, "3s")
	create(t, busy)
	waitCondition(t, busy, AppliedCondition, metav1.ConditionFalse, ReasonTargetBusy)

	waitCondition(t, pf, RevertedCondition, metav1.ConditionTrue, ReasonExpired)
	podUntouched(t, "tac-pf", "web-0")
}

func TestPodFailureRevertedOnDelete(t *testing.T) {
	ctx := context.Background()
	deploymentNamespace(t, "tac-pfd", "pfd")
	newPod(t, "tac-pfd", "web-0", nil)

	pf := newPodFault("tac-pfd", "fail", deploymentproxyv1.PodFailure, deploymentproxyv1.PodTarget{Pod: "web-0"}, "5m")
	create(t, pf)
	got := waitCondition(t, pf, AppliedCondition, metav1.ConditionTrue, ReasonApplied)
	p, err := getPod("tac-pfd", "web-0")
	if err != nil {
		t.Fatal(err)
	}
	if err := failed(p, got); err != nil {
		t.Fatal(err)
	}
	if err := admin.Delete(ctx, pf); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() error {
		if _, err := getPod("tac-pfd", "web-0"); err != nil {
			return err
		}
		p, _ := getPod("tac-pfd", "web-0")
		if _, ok := p.Labels[PodFaultLabel]; ok || p.Spec.Containers[0].Image != "example.com/app:1" {
			return fmt.Errorf("pod web-0 is not reverted: %v %v", p.Labels, p.Spec.Containers)
		}
		return nil
	})
	podUntouched(t, "tac-pfd", "web-0")
}

func TestPodFaultTargetGone(t *testing.T) {
	ctx := context.Background()
	deploymentNamespace(t, "tac-pg", "pg")
	pod := newPod(t, "tac-pg", "web-0", nil)

	pf := newPodFault("tac-pg", "fail", deploymentproxyv1.PodFailure, deploymentproxyv1.PodTarget{Pod: "web-0"}, "2s")
	create(t, pf)
	waitCondition(t, pf, AppliedCondition, metav1.ConditionTrue, ReasonApplied)
	if err := admin.Delete(ctx, pod); err != nil {
		t.Fatal(err)
	}
	waitCondition(t, pf, RevertedCondition, metav1.ConditionTrue, ReasonTargetGone)
}

func TestChaosRBAC(t *testing.T) {
	ctx := context.Background()
	deploymentNamespace(t, "tac-chx", "chx")
	deploymentNamespace(t, "tac-chy", "chy")
	if err := applyFile(ctx, admin, "../../config/deployment-namespace/chaos.yaml", map[string]string{"${NAMESPACE}": "tac-chx"}); err != nil {
		t.Fatal(err)
	}
	chaos, err := client.New(impersonate(adminCfg, "tac-chx", "chaos-controller"), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	operator, err := client.New(impersonate(adminCfg, "tac-chx", "operator"), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	newPod(t, "tac-chx", "web-0", nil)

	pf := newPodFault("tac-chx", "kill", deploymentproxyv1.PodKill, deploymentproxyv1.PodTarget{Pod: "web-0"}, "1m")
	if err := chaos.Create(ctx, pf); err != nil {
		t.Fatalf("chaos-controller creating in its own namespace: %v", err)
	}
	got := waitCondition(t, pf, AppliedCondition, metav1.ConditionTrue, ReasonApplied)

	// Only deploymentproxy writes status.
	got.Status.Pod = "other"
	if err := chaos.Status().Update(ctx, got); !apierrors.IsForbidden(err) {
		t.Errorf("chaos-controller writing status: got %v, want Forbidden", err)
	}
	// It may not touch pods itself, nor ask from another deployment's namespace; nobody else may ask.
	if err := chaos.Delete(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "tac-chx", Name: "web-0"}}); !apierrors.IsForbidden(err) {
		t.Errorf("chaos-controller deleting a pod: got %v, want Forbidden", err)
	}
	if err := chaos.Create(ctx, newPodFault("tac-chy", "kill", deploymentproxyv1.PodKill, deploymentproxyv1.PodTarget{Pod: "x"}, "1m")); !apierrors.IsForbidden(err) {
		t.Errorf("chaos-controller creating in tac-nu: got %v, want Forbidden", err)
	}
	if err := operator.Create(ctx, newPodFault("tac-chx", "op", deploymentproxyv1.PodKill, deploymentproxyv1.PodTarget{Pod: "x"}, "1m")); !apierrors.IsForbidden(err) {
		t.Errorf("operator creating a PodFault: got %v, want Forbidden", err)
	}
	if err := chaos.Delete(ctx, pf); err != nil {
		t.Errorf("chaos-controller deleting its PodFault: %v", err)
	}
}

// Marks on a pod are pod metadata anyone with rights on the pod can write: deploymentproxy reverts from the
// PodFault's status only, so a forged mark neither blocks another deployment's fault nor makes deploymentproxy
// write images it chose.
func TestPodFaultForgedMarks(t *testing.T) {
	deploymentNamespace(t, "tac-fg", "fg")
	newPod(t, "tac-fg", "web-0", nil)
	pf := newPodFault("tac-fg", "fail", deploymentproxyv1.PodFailure, deploymentproxyv1.PodTarget{Pod: "web-0"}, "2s")
	deploymentNamespace(t, "tac-fh", "fh")
	newPod(t, "tac-fh", "forged", nil, func(p *corev1.Pod) {
		p.Labels = map[string]string{PodFaultLabel: "anything"}
		p.Annotations = map[string]string{PodFaultAnnotation: "tac-fg/fail", "deploymentproxy.tacplus.dev/original-images": `{"app":"evil.example.com/x:1"}`}
	})

	create(t, pf)
	got := waitCondition(t, pf, AppliedCondition, metav1.ConditionTrue, ReasonApplied)
	p, err := getPod("tac-fg", "web-0")
	if err != nil {
		t.Fatal(err)
	}
	if err := failed(p, got); err != nil {
		t.Fatal(err)
	}
	if got.Status.PodUID != p.UID || got.Status.OriginalImages["app"] != "example.com/app:1" || got.Status.OriginalImages["sidecar"] != "example.com/sidecar:2" {
		t.Errorf("status = %+v", got.Status)
	}
	waitCondition(t, pf, RevertedCondition, metav1.ConditionTrue, ReasonExpired)
	podUntouched(t, "tac-fg", "web-0")

	forged, err := getPod("tac-fh", "forged")
	if err != nil {
		t.Fatal(err)
	}
	if forged.Spec.Containers[0].Image != "example.com/app:1" || forged.Labels[PodFaultLabel] != "anything" {
		t.Errorf("the forged pod was changed: %v %v", forged.Labels, forged.Spec.Containers)
	}
	// A pod marked as another fault's is not taken.
	busy := newPodFault("tac-fh", "busy", deploymentproxyv1.PodFailure, deploymentproxyv1.PodTarget{Pod: "forged"}, "2s")
	create(t, busy)
	waitCondition(t, busy, AppliedCondition, metav1.ConditionFalse, ReasonTargetBusy)
}

// Status is deploymentproxy's to write, but every action on the pod it records is checked against the scope again.
func TestPodFaultForgedStatus(t *testing.T) {
	ctx := context.Background()
	deploymentNamespace(t, "tac-fs", "fs")
	victim := newPod(t, "kube-system", "fs-victim", nil)
	pf := newPodFault("tac-fs", "kill", deploymentproxyv1.PodKill, deploymentproxyv1.PodTarget{Pod: "nobody"}, "10s")
	create(t, pf)
	got := waitCondition(t, pf, AppliedCondition, metav1.ConditionFalse, ReasonTargetNotFound)

	before := got.DeepCopy()
	got.Status.Namespace, got.Status.Pod, got.Status.PodUID = "kube-system", "fs-victim", victim.UID
	meta.SetStatusCondition(&got.Status.Conditions, metav1.Condition{Type: AppliedCondition, Status: metav1.ConditionUnknown, Reason: ReasonApplying, Message: "forged"})
	if err := admin.Status().Patch(ctx, got, client.MergeFrom(before)); err != nil {
		t.Fatal(err)
	}
	waitCondition(t, pf, AppliedCondition, metav1.ConditionFalse, ReasonExcludedNamespace)
	podUntouched(t, "kube-system", "fs-victim")
}

// TestRandomIndex checks the index choosePod picks an eligible pod by stays in range and reaches every pod.
func TestRandomIndex(t *testing.T) {
	const n = 3
	seen := map[int]bool{}
	for range 300 {
		i := randomIndex(n)
		if i < 0 || i >= n {
			t.Fatalf("randomIndex(%d) = %d, out of range", n, i)
		}
		seen[i] = true
	}
	if len(seen) != n {
		t.Errorf("randomIndex(%d) reached %v in 300 draws, not every index", n, seen)
	}
	if i := randomIndex(1); i != 0 {
		t.Errorf("randomIndex(1) = %d, want 0", i)
	}
}
