# Multi-stage build for llm-d-inference-payload-processor
# Supports multi-arch: linux/amd64, linux/arm64

# --- Build stage ---
FROM golang:1.26 AS builder

WORKDIR /workspace

# Cache dependencies (go.sum is optional until the first dep is added)
COPY go.mod ./
COPY go.su[m] ./
RUN go mod download

# Copy source and build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /workspace/app ./cmd

# --- Runtime stage ---
FROM gcr.io/distroless/static:nonroot

WORKDIR /
COPY --from=builder /workspace/app .

USER 65532:65532

ENTRYPOINT ["/app"]
