## Build the manager binary
FROM --platform=$BUILDPLATFORM golang:1.24 as builder

ARG TARGETPLATFORM
ARG BUILDPLATFORM
ARG TARGETARCH
ENV GOPROXY=https://goproxy.cn,direct

WORKDIR /workspace

# Copy go mod files first for better layer caching
COPY go.mod go.mod
COPY go.sum go.sum

RUN go mod download

# Copy source code
COPY cmd/ cmd/
COPY pkg/ pkg/
COPY hack/ hack/

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -a -o bin/controller cmd/controller/main.go

FROM registry-cn-hangzhou.ack.aliyuncs.com/dev/debian:12-slim-update
RUN apt-get update && apt-get upgrade -y && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/* /var/cache/apt/*
WORKDIR /
COPY --from=builder /workspace/bin/controller .
USER 65532:65532

# Expose metrics and webhook ports
EXPOSE 8080 9443

# Run the controller
ENTRYPOINT ["/controller"]