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
// The build stage's base is pinned by digest (#27): the build is the same until the digest moves, and moving it to a
// rebuilt base is how a fixed Alpine package (zlib's CVE-2026-4738) arrives.
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
	if build := regexp.MustCompile(`^FROM --platform=\$BUILDPLATFORM golang:1\.[0-9]+-alpine@sha256:[0-9a-f]{64} AS build$`); !build.MatchString(froms[0]) {
		t.Errorf("build stage is %q, want golang:<line>-alpine@sha256:<digest> on $BUILDPLATFORM, AS build", froms[0])
	}
	if !strings.Contains(string(b), "CGO_ENABLED=0 ") {
		t.Error("the build is not CGO_ENABLED=0: the binary must be static to run on distroless/static")
	}
	if want := "FROM gcr.io/distroless/static-debian13:nonroot"; froms[1] != want {
		t.Errorf("runtime stage is %q, want %q", froms[1], want)
	}
}

// TestDockerfileBuildStageOpenSSL proves the build stage runs on a fixed OpenSSL while the pinned base still ships
// 3.5.9-r0 (#29): it upgrades libssl3 and libcrypto3 right after its FROM, before anything else runs. It also proves
// the runtime stage carries none of the build stage's packages: the binary is the one thing it copies from it.
func TestDockerfileBuildStageOpenSSL(t *testing.T) {
	b, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	stage := -1
	var build, copies []string
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "FROM ") {
			stage++
			continue
		}
		if stage == 0 {
			build = append(build, line)
		}
		if strings.Contains(line, "--from=") {
			copies = append(copies, line)
		}
	}
	if want := "RUN apk upgrade --no-cache libssl3 libcrypto3"; len(build) == 0 || build[0] != want {
		t.Errorf("build stage's instructions are %q, want %q first", build, want)
	}
	if want := "COPY --from=build /deploymentproxy /deploymentproxy"; len(copies) != 1 || copies[0] != want {
		t.Errorf("the runtime stage copies %q from the build stage, want only %q", copies, want)
	}
}
