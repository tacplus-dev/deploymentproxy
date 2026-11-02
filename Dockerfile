# Built on the build host's platform and cross-compiled, so a multi-architecture build needs no emulation.
FROM --platform=$BUILDPLATFORM golang:1.27 AS build
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
