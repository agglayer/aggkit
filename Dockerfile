# ================================
# STAGE 1: Build binary
# ================================
FROM --platform=${BUILDPLATFORM} golang:1.24.4-alpine AS builder

# Install build dependencies
RUN apk add --no-cache gcc make musl-dev sqlite-dev

# Create non-root user with home directory
RUN addgroup appgroup && adduser -D -G appgroup -h /home/appuser appuser && \
    mkdir -p /app && chown -R appuser:appgroup /app

USER appuser
WORKDIR /app

# Download Go dependencies
COPY --chown=appuser:appgroup go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY --chown=appuser:appgroup . .
RUN make build-aggkit

# ================================
# STAGE 2: Final runtime image (distroless)
# ================================
FROM gcr.io/distroless/static-debian12:nonroot

# Copy binary (already built for correct architecture)
COPY --chown=nonroot:nonroot --from=builder /app/target/aggkit /usr/local/bin/aggkit

# Expose API port
EXPOSE 5576/tcp

# Run as non-root user (distroless uses UID 65532 `nonroot`)
USER nonroot

# Set entrypoint
ENTRYPOINT ["/usr/local/bin/aggkit"]
