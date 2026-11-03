package deploymentproxy

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

// TestPodTemplatesSelectArm64 proves every pod deploymentproxy's manifests run schedules on arm64 nodes (#11):
// GKE taints Arm nodes kubernetes.io/arch=arm64:NoSchedule and adds the toleration only to a pod that selects
// arm64, so every pod template carries both the nodeSelector and the explicit toleration, which keeps it working
// on a cluster that does not add one.
func TestPodTemplatesSelectArm64(t *testing.T) {
	paths, err := filepath.Glob("config/*/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	want := corev1.Toleration{Key: "kubernetes.io/arch", Operator: corev1.TolerationOpEqual, Value: "arm64", Effect: corev1.TaintEffectNoSchedule}
	templates := 0
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for i, doc := range regexp.MustCompile(`(?m)^---\s*$`).Split(string(b), -1) {
			if strings.TrimSpace(doc) == "" {
				continue
			}
			var meta struct{ Kind string }
			if err := yaml.Unmarshal([]byte(doc), &meta); err != nil {
				t.Fatalf("%s, document %d: %v", path, i, err)
			}
			switch meta.Kind {
			case "Deployment", "DaemonSet", "StatefulSet":
			case "Pod", "Job", "CronJob", "ReplicaSet":
				t.Errorf("%s, document %d: a %s: add it to this test", path, i, meta.Kind)
				continue
			default:
				continue
			}
			// The three kinds have their pod template at spec.template.
			var w struct {
				Spec struct{ Template corev1.PodTemplateSpec }
			}
			if err := yaml.Unmarshal([]byte(doc), &w); err != nil {
				t.Fatalf("%s, document %d: %v", path, i, err)
			}
			spec := w.Spec.Template.Spec
			templates++
			if got := spec.NodeSelector["kubernetes.io/arch"]; got != "arm64" {
				t.Errorf("%s, %s: nodeSelector kubernetes.io/arch is %q, want arm64", path, meta.Kind, got)
			}
			found := false
			for _, tol := range spec.Tolerations {
				found = found || tol == want
			}
			if !found {
				t.Errorf("%s, %s: no toleration %+v", path, meta.Kind, want)
			}
		}
	}
	if templates == 0 {
		t.Fatal("no pod template found under config/")
	}
}
