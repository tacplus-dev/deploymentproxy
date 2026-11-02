// Package v1 is the deploymentproxy.tacplus.dev/v1 API: the TenantNamespace resource.
package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GroupVersion is the API group and version of TenantNamespace.
var GroupVersion = schema.GroupVersion{Group: "deploymentproxy.tacplus.dev", Version: "v1"}

var schemeBuilder = runtime.NewSchemeBuilder(func(s *runtime.Scheme) error {
	s.AddKnownTypes(GroupVersion, &TenantNamespace{}, &TenantNamespaceList{})
	metav1.AddToGroupVersion(s, GroupVersion)
	return nil
})

// AddToScheme registers the deploymentproxy.tacplus.dev/v1 types.
var AddToScheme = schemeBuilder.AddToScheme
