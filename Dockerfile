# ================================
# STAGE 1: Build binary
# ================================
FROM --platform=${BUILDPLATFORM} golang:1.24.4-alpine AS builder

# Install build dependencies
RUN apk add --no-cache gcc musl-dev make sqlite-dev

WORKDIR /app

# Download Go dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .

# Compile binary
RUN make build-aggkit build-tools

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
      rm -f /bin/sh /bin/bash /bin/ash; \
    fi

# Set the working directory and user
WORKDIR /home/appuser
USER appuser

# Copy the built binary from the builder stage
COPY --from=builder /app/target/aggkit /usr/local/bin/aggkit

EXPOSE 5576/tcp

ENTRYPOINT ["/usr/local/bin/aggkit"]
