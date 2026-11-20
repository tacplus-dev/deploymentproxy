# Built on the build host's platform and cross-compiled, so a multi-architecture build needs no emulation. The base is
# pinned by digest (#27; golang:1.27-alpine, go1.27.4 on Alpine 3.24, of 2026-11-10): a fixed Alpine package arrives
# when the digest moves to a rebuilt image (`docker buildx imagetools inspect golang:1.27-alpine` prints it).
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS build
# This digest ships OpenSSL 3.5.9-r0; Alpine's 3.5.9-r1 fixes five CVEs (#29), and no rebuilt image has it yet. The
# security run scans the FROM image by name, so it lists them until the digest moves; this stage builds on the fix.
RUN apk upgrade --no-cache libssl3 libcrypto3
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY api api
COPY cmd cmd
COPY internal internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags=-s -o /deploymentproxy ./cmd/deploymentproxy

FROM gcr.io/distroless/static-debian13:nonroot
# Set by the release (.github/workflows/release.yml).
ARG VERSION=dev
ARG REVISION=unknown
LABEL org.opencontainers.image.source="https://github.com/tacplus-dev/deploymentproxy" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.revision="$REVISION"
COPY --from=build /deploymentproxy /deploymentproxy
USER 65532:65532
ENTRYPOINT ["/deploymentproxy"]
