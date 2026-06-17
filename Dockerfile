# BASE_IMAGE can be overridden at build time, e.g.:
#   --build-arg BASE_IMAGE=registry.access.redhat.com/ubi9/ubi-micro:9.7
# Default is distroless/static which includes CA certs and has minimal CVE surface.
ARG BASE_IMAGE=gcr.io/distroless/static:nonroot

# Go build stage
FROM --platform=${BUILDPLATFORM} golang:1.25 AS go-builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum

# Download dependencies - this layer is cached as long as go.mod/go.sum are unchanged
RUN go mod download

# Copy the go source
COPY apix/     apix/
COPY cmd/      cmd/
COPY pkg/      pkg/
COPY internal/ internal/
COPY version/  version/

# Precompile without version flags so commit-only changes can reuse the
# expensive compile work from the Docker layer cache.
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -o /dev/null ./cmd

# Build with version metadata.
ARG COMMIT_SHA=unknown
ARG BUILD_REF
ARG LDFLAGS="-s -w"
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -ldflags="${LDFLAGS} -X github.com/llm-d/llm-d-inference-payload-processor/version.CommitSHA=${COMMIT_SHA} -X github.com/llm-d/llm-d-inference-payload-processor/version.BuildRef=${BUILD_REF}" \
    -o bin/payload-processor ./cmd

# Runtime stage
FROM ${BASE_IMAGE}

WORKDIR /

COPY --from=go-builder /workspace/bin/payload-processor /app/payload-processor

USER 65532:65532

# expose gRPC, health and metrics ports
EXPOSE 9004
EXPOSE 9005
EXPOSE 9090

ENTRYPOINT ["/app/payload-processor"]
