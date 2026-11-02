package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// TenantNamespace asks deploymentproxy for the tenant namespace tac-<deployment>-<tid>. It lives in a deployment
// namespace (labelled tacplus.dev/deployment); deleting it deletes the tenant namespace.
type TenantNamespace struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TenantNamespaceSpec   `json:"spec"`
	Status TenantNamespaceStatus `json:"status,omitempty"`
}

// TenantNamespaceSpec is what the deployment's operator asks for.
type TenantNamespaceSpec struct {
	// Tid is the tenant id: exactly 8 characters of [0-9a-z], immutable. An all-digit tid is reserved for
	// the platform and accepted only for the reserved ids deploymentproxy is configured with.
	Tid string `json:"tid"`
}

// TenantNamespaceStatus is written by deploymentproxy only.
type TenantNamespaceStatus struct {
	// Namespace is the name of the tenant namespace, once deploymentproxy has it in place.
	Namespace string `json:"namespace,omitempty"`
	// Conditions holds Ready; when it is False, its reason says why.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// TenantNamespaceList is a list of TenantNamespace.
type TenantNamespaceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TenantNamespace `json:"items"`
}

// DeepCopyInto copies in into out.
func (in *TenantNamespace) DeepCopyInto(out *TenantNamespace) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec = in.Spec
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy returns a deep copy.
func (in *TenantNamespace) DeepCopy() *TenantNamespace {
	if in == nil {
		return nil
	}
	out := new(TenantNamespace)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject implements runtime.Object.
func (in *TenantNamespace) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

// DeepCopyInto copies in into out.
func (in *TenantNamespaceStatus) DeepCopyInto(out *TenantNamespaceStatus) {
	*out = *in
	if in.Conditions != nil {
		out.Conditions = make([]metav1.Condition, len(in.Conditions))
		for i := range in.Conditions {
			in.Conditions[i].DeepCopyInto(&out.Conditions[i])
		}
	}
}

// DeepCopyInto copies in into out.
func (in *TenantNamespaceList) DeepCopyInto(out *TenantNamespaceList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]TenantNamespace, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

// DeepCopy returns a deep copy.
func (in *TenantNamespaceList) DeepCopy() *TenantNamespaceList {
	if in == nil {
		return nil
	}
	out := new(TenantNamespaceList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject implements runtime.Object.
func (in *TenantNamespaceList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}
