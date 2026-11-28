package deploymentproxy

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestDockerfileStages proves the image is built on golang's Alpine variant and ships on distroless (#23): the build
// stage only downloads modules and cross-compiles a static binary, so the Debian variant's packages (kernel headers,
// krb5, openssh, git, python, perl, curl) are never needed, and the security run fails the policy on their advisories.
func TestDockerfileStages(t *testing.T) {
	b, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	var froms []string
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "FROM ") {
			froms = append(froms, line)
		}
	}
	if len(froms) != 2 {
		t.Fatalf("Dockerfile has %d stages, want 2 (build and runtime): %q", len(froms), froms)
	}
	if build := regexp.MustCompile(`^FROM --platform=\$BUILDPLATFORM golang:1\.[0-9]+-alpine AS build$`); !build.MatchString(froms[0]) {
		t.Errorf("build stage is %q, want golang:<line>-alpine on $BUILDPLATFORM, AS build", froms[0])
	}
	if !strings.Contains(string(b), "CGO_ENABLED=0 ") {
		t.Error("the build is not CGO_ENABLED=0: the binary must be static to run on distroless/static")
	}
	if want := "FROM gcr.io/distroless/static-debian13:nonroot"; froms[1] != want {
		t.Errorf("runtime stage is %q, want %q", froms[1], want)
	}
}
