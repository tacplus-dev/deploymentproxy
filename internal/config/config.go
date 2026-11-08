// Package config is deploymentproxy's configuration: the reserved tids it accepts, the quota it gives each
// deployment's tenant namespaces, and what chaos faults may not target. It is read from the deploymentproxy
// ConfigMap, mounted as a file.
package config

import (
	"fmt"
	"os"
	"regexp"
	"slices"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/yaml"
)

var (
	tidPattern      = regexp.MustCompile(`^[0-9a-z]{8}$`)
	reservedPattern = regexp.MustCompile(`^[0-9]{8}$`)
)

// Config is the content of the ConfigMap's config.yaml.
type Config struct {
	// ReservedTids are the all-digit tids deploymentproxy accepts (people is 00000001). Every other all-digit tid
	// is refused.
	ReservedTids []string `json:"reservedTids,omitempty"`
	// SharedTids has no effect: it chose the shared tenants of the NetworkPolicies deploymentproxy no longer
	// keeps (#14). It is still accepted, and checked to be reserved tids, so that existing ConfigMaps load.
	SharedTids []string `json:"sharedTids,omitempty"`
	Quota      Quota    `json:"quota,omitempty"`
	Chaos      Chaos    `json:"chaos,omitempty"`
}

// DefaultFailureImage is the image a PodFault of action Failure gives each container, unless configured.
const DefaultFailureImage = "registry.k8s.io/pause:3.10"

// Chaos configures the fault kinds. The exclusions only add to the compiled-in ones (internal/controller).
type Chaos struct {
	// ExcludedNamespaces are namespaces no fault targets, even one of the requesting deployment's.
	ExcludedNamespaces []string `json:"excludedNamespaces,omitempty"`
	// ExcludedServices are values of a pod's app.kubernetes.io/name label no fault targets.
	ExcludedServices []string `json:"excludedServices,omitempty"`
	// FailureImage replaces DefaultFailureImage.
	FailureImage string `json:"failureImage,omitempty"`
}

// Quota is the ResourceQuota of a tenant namespace: Default, with a deployment's entry in Deployments
// merged over it. services.loadbalancers and services.nodeports are always 0, whatever is configured.
type Quota struct {
	Default     corev1.ResourceList            `json:"default,omitempty"`
	Deployments map[string]corev1.ResourceList `json:"deployments,omitempty"`
}

// Load reads and validates the config file at path.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// Parse parses and validates config.yaml.
func Parse(b []byte) (*Config, error) {
	c := &Config{}
	if err := yaml.UnmarshalStrict(b, c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	for _, tid := range c.ReservedTids {
		if !tidPattern.MatchString(tid) || !reservedPattern.MatchString(tid) {
			return nil, fmt.Errorf("config: reserved tid %q is not 8 digits", tid)
		}
	}
	for _, tid := range c.SharedTids {
		if !slices.Contains(c.ReservedTids, tid) {
			return nil, fmt.Errorf("config: shared tid %q is not a reserved tid", tid)
		}
	}
	if c.Chaos.FailureImage == "" {
		c.Chaos.FailureImage = DefaultFailureImage
	}
	return c, nil
}

// ValidTid reports whether deploymentproxy accepts tid: 8 characters of [0-9a-z], and, if all digits, reserved.
func (c *Config) ValidTid(tid string) bool {
	if !tidPattern.MatchString(tid) {
		return false
	}
	if !reservedPattern.MatchString(tid) {
		return true
	}
	return slices.Contains(c.ReservedTids, tid)
}

// QuotaFor returns the hard limits of a tenant namespace of deployment.
func (c *Config) QuotaFor(deployment string) corev1.ResourceList {
	hard := corev1.ResourceList{}
	for k, v := range c.Quota.Default {
		hard[k] = v.DeepCopy()
	}
	for k, v := range c.Quota.Deployments[deployment] {
		hard[k] = v.DeepCopy()
	}
	hard[corev1.ResourceServicesLoadBalancers] = resource.MustParse("0")
	hard[corev1.ResourceServicesNodePorts] = resource.MustParse("0")
	return hard
}
