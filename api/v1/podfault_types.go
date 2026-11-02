package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

// PodFaultAction is what a PodFault does to its target pod.
type PodFaultAction string

const (
	// PodKill deletes the pod, once; its owner recreates it.
	PodKill PodFaultAction = "Kill"
	// PodFailure sets every container of the pod to the failure image for the fault's duration, so the pod stays
	// scheduled but does not serve; the images are put back at the end.
	PodFailure PodFaultAction = "Failure"
)

// PodFault asks deploymentproxy for a chaos fault on one pod of the requesting deployment. It lives in a deployment
// namespace (labelled tacplus.dev/deployment) and may target only that namespace or one of its tenant
// namespaces. Its spec is immutable; deleting it reverts the fault.
type PodFault struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PodFaultSpec   `json:"spec"`
	Status PodFaultStatus `json:"status,omitempty"`
}

// PodFaultSpec is what the deployment's chaos controller asks for.
type PodFaultSpec struct {
	// Tag is the fault's tag, opaque to deploymentproxy and copied unchanged to status.tag.
	Tag string `json:"tag,omitempty"`
	// Action is Kill or Failure.
	Action PodFaultAction `json:"action"`
	// Target chooses the pod.
	Target PodTarget `json:"target"`
	// Duration is how long the fault lasts, a Go duration ("30s", "2m"), 1s to 5m. A Kill has nothing to put
	// back: its end only marks it reverted.
	Duration string `json:"duration"`
	// GracePeriodSeconds is a Kill's grace period: 0 to 30, default 0. Not allowed with Failure.
	GracePeriodSeconds *int64 `json:"gracePeriodSeconds,omitempty"`
}

// PodTarget chooses one pod: by name, or one of those a label selector matches.
type PodTarget struct {
	// Namespace is the deployment namespace or one of its tenant namespaces; default the PodFault's own.
	Namespace string `json:"namespace,omitempty"`
	// Pod is the pod's name. Exactly one of Pod and Selector is set.
	Pod string `json:"pod,omitempty"`
	// Selector matches pods in Namespace; deploymentproxy chooses one of those it may target.
	Selector *metav1.LabelSelector `json:"selector,omitempty"`
}

// PodFaultStatus is written by deploymentproxy only.
type PodFaultStatus struct {
	// Tag is spec.tag, unchanged.
	Tag string `json:"tag,omitempty"`
	// Namespace, Pod and PodUID name the target pod, once one is chosen; deploymentproxy acts on and reverts that
	// pod only.
	Namespace string    `json:"namespace,omitempty"`
	Pod       string    `json:"pod,omitempty"`
	PodUID    types.UID `json:"podUID,omitempty"`
	// OriginalImages are the images a Failure replaces, by container name: what its revert puts back.
	OriginalImages map[string]string `json:"originalImages,omitempty"`
	// StartTime is when the fault was applied, EndTime when it is (or was) reverted.
	StartTime *metav1.Time `json:"startTime,omitempty"`
	EndTime   *metav1.Time `json:"endTime,omitempty"`
	// Conditions holds Applied and Reverted. A refused fault has Applied False with the refusal as its reason; a
	// fault being applied has Applied Unknown.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// PodFaultList is a list of PodFault.
type PodFaultList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PodFault `json:"items"`
}

// DeepCopyInto copies in into out.
func (in *PodFault) DeepCopyInto(out *PodFault) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy returns a deep copy.
func (in *PodFault) DeepCopy() *PodFault {
	if in == nil {
		return nil
	}
	out := new(PodFault)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject implements runtime.Object.
func (in *PodFault) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

// DeepCopyInto copies in into out.
func (in *PodFaultSpec) DeepCopyInto(out *PodFaultSpec) {
	*out = *in
	if in.Target.Selector != nil {
		out.Target.Selector = in.Target.Selector.DeepCopy()
	}
	if in.GracePeriodSeconds != nil {
		g := *in.GracePeriodSeconds
		out.GracePeriodSeconds = &g
	}
}

// DeepCopyInto copies in into out.
func (in *PodFaultStatus) DeepCopyInto(out *PodFaultStatus) {
	*out = *in
	if in.OriginalImages != nil {
		out.OriginalImages = make(map[string]string, len(in.OriginalImages))
		for k, v := range in.OriginalImages {
			out.OriginalImages[k] = v
		}
	}
	if in.StartTime != nil {
		out.StartTime = in.StartTime.DeepCopy()
	}
	if in.EndTime != nil {
		out.EndTime = in.EndTime.DeepCopy()
	}
	if in.Conditions != nil {
		out.Conditions = make([]metav1.Condition, len(in.Conditions))
		for i := range in.Conditions {
			in.Conditions[i].DeepCopyInto(&out.Conditions[i])
		}
	}
}

// DeepCopyInto copies in into out.
func (in *PodFaultList) DeepCopyInto(out *PodFaultList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]PodFault, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

// DeepCopy returns a deep copy.
func (in *PodFaultList) DeepCopy() *PodFaultList {
	if in == nil {
		return nil
	}
	out := new(PodFaultList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject implements runtime.Object.
func (in *PodFaultList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}
