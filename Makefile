# The gates: make check (formatting, vet, build, tests). Tests run a real API server (envtest).
ENVTEST_K8S_VERSION ?= 1.37.x
SETUP_ENVTEST ?= go run sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.25

.PHONY: check fmt vet build test image

check: fmt vet build test

fmt:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "gofmt: the files above need formatting"; exit 1; }

vet:
	go vet ./...

build:
	go build ./...

test:
	KUBEBUILDER_ASSETS="$$($(SETUP_ENVTEST) use -p path $(ENVTEST_K8S_VERSION))" go test -count=1 ./...

image:
	docker build -t ghcr.io/tacplus-dev/deploymentproxy:latest .
