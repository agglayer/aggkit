# ================================
# STAGE 1: Build binary
# ================================
FROM golang:1.25.7-alpine AS builder

# Install build dependencies
RUN apk add --no-cache gcc musl-dev make sqlite-dev git

WORKDIR /app

# Download Go dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .

# Compile binary
RUN make build-aggkit build-aggkit-proxy build-tools

# ================================
# STAGE 2: Final runtime image
# ================================
FROM alpine:3.22

# Build argument to control shell installation
ARG INCLUDE_SHELL=false

# Install runtime dependencies
RUN apk add --no-cache sqlite-libs ca-certificates && \
    if [ "$INCLUDE_SHELL" = "true" ]; then \
        echo "Including shell and sqlite CLI for CI/dev environment" && \
        apk add --no-cache sqlite procps; \
    fi

# Add non-root user (as before)
RUN addgroup appgroup && \
    if [ "$INCLUDE_SHELL" = "true" ]; then \
        adduser -D -G appgroup -h /home/appuser -s /bin/ash appuser; \
    else \
        adduser -D -G appgroup -h /home/appuser -s /bin/false appuser; \
    fi && \
    mkdir -p /home/appuser && \
    chown -R appuser:appgroup /home/appuser

# Remove shell for production security (only if not INCLUDE_SHELL)
RUN if [ "$INCLUDE_SHELL" != "true" ]; then \
      echo "Removing shell for production security" && \
      rm -f /bin/sh /bin/bash /bin/ash /bin/busybox; \
    fi

# Set the working directory and user
WORKDIR /home/appuser
USER appuser

# Copy the built binary from the builder stage
COPY --from=builder /app/target/aggkit /usr/local/bin/aggkit
COPY --from=builder /app/target/aggkit-proxy /usr/local/bin/aggkit-proxy
COPY --from=builder /app/target/aggsender_find_imported_bridge /usr/local/bin/aggsender_find_imported_bridge

# Exit certificate tooling: the generator and the claimer HTTP service.
COPY --from=builder /app/target/exit_certificate /usr/local/bin/exit_certificate
COPY --from=builder /app/target/exit_certificate_claimer /usr/local/bin/exit_certificate_claimer

# Long-running tool that forces L1 Global Exit Root updates when none happen organically.
COPY --from=builder /app/target/force_ger_update /usr/local/bin/force_ger_update

EXPOSE 5576/tcp

ENTRYPOINT ["/usr/local/bin/aggkit"]
