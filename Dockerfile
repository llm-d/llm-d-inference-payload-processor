# Dockerfile has specific requirement to put this ARG at the beginning:
# https://docs.docker.com/engine/reference/builder/#understand-how-arg-and-from-interact
ARG BUILDER_IMAGE=golang:1.25
ARG BASE_IMAGE=gcr.io/distroless/static:nonroot

## Multistage build
FROM ${BUILDER_IMAGE} AS builder
ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64
ARG COMMIT_SHA=unknown
ARG BUILD_REF

# Dependencies
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

# Sources
COPY cmd ./cmd
COPY pkg ./pkg
COPY internal ./internal
COPY version ./version
WORKDIR /src/cmd
RUN go build -ldflags="-X github.com/llm-d/llm-d-inference-payload-processor/version.CommitSHA=${COMMIT_SHA} -X github.com/llm-d/llm-d-inference-payload-processor/version.BuildRef=${BUILD_REF}" -o /payload-processor

## Multistage deploy
FROM ${BASE_IMAGE}

WORKDIR /
COPY --from=builder /payload-processor /payload-processor

ENTRYPOINT ["/payload-processor"]
