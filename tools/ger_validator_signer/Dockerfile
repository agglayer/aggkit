# Image of the GER validator signer. The build context is this directory (tools/ger_validator_signer):
#
#   docker build -t ger_validator_signer:local tools/ger_validator_signer
#   make build-docker-ger_validator_signer
#
# It is its own Go module and its own image, separate from the aggkit one.

# ================================
# STAGE 1: Build binary
# ================================
FROM golang:1.27.3-alpine AS builder

# CGO is needed: the GCP KMS signer of go_signer (op-signer) uses the C implementation of secp256k1
RUN apk add --no-cache gcc musl-dev git

WORKDIR /app

# Download Go dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .

ARG VERSION=v0.0.0-dev
ARG GIT_REV=unknown
ARG GIT_BRANCH=unknown
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=1 go build -trimpath \
    -ldflags "-X 'github.com/agglayer/aggkit/tools/ger_validator_signer/version.Version=${VERSION}' \
              -X 'github.com/agglayer/aggkit/tools/ger_validator_signer/version.GitRev=${GIT_REV}' \
              -X 'github.com/agglayer/aggkit/tools/ger_validator_signer/version.GitBranch=${GIT_BRANCH}' \
              -X 'github.com/agglayer/aggkit/tools/ger_validator_signer/version.BuildDate=${BUILD_DATE}'" \
    -o /out/ger_validator_signer ./cmd

# ================================
# STAGE 2: Final runtime image
# ================================
FROM alpine:3.22

# Build argument to control shell installation (for CI/dev images)
ARG INCLUDE_SHELL=false

# CA certificates: the validator talks HTTPS to the L1 RPC and to the KMS
RUN apk add --no-cache ca-certificates

# Non-root user
RUN addgroup appgroup && \
    if [ "$INCLUDE_SHELL" = "true" ]; then \
        adduser -D -G appgroup -h /home/appuser -s /bin/ash appuser; \
    else \
        adduser -D -G appgroup -h /home/appuser -s /bin/false appuser; \
    fi && \
    mkdir -p /home/appuser && \
    chown -R appuser:appgroup /home/appuser

# Remove the shell for production security (only if not INCLUDE_SHELL)
RUN if [ "$INCLUDE_SHELL" != "true" ]; then \
      echo "Removing shell for production security" && \
      rm -f /bin/sh /bin/bash /bin/ash /bin/busybox; \
    fi

WORKDIR /home/appuser
USER appuser

COPY --from=builder /out/ger_validator_signer /usr/local/bin/ger_validator_signer

# The REST API
EXPOSE 8080/tcp

ENTRYPOINT ["/usr/local/bin/ger_validator_signer"]
CMD ["run", "--cfg", "/etc/ger_validator_signer/config.toml"]
