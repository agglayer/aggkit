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
RUN make build-aggkit

# ================================
# STAGE 2: Final runtime image
# ================================
FROM alpine:3.22

# Install runtime dependencies and remove shell
RUN apk add --no-cache sqlite-libs ca-certificates

# Add non-root user with home and nologin shell
RUN addgroup appgroup && \
    adduser -D -G appgroup -h /home/appuser -s /sbin/nologin appuser && \
    mkdir -p /home/appuser && \
    chown -R appuser:appgroup /home/appuser && \
    rm -f /bin/sh

# Set the working directory and user
# This ensures that the application runs as a non-root user
WORKDIR /home/appuser
USER appuser

# Copy the built binary from the builder stage
COPY --from=builder /app/target/aggkit /usr/local/bin/aggkit

EXPOSE 5576/tcp

ENTRYPOINT ["/usr/local/bin/aggkit"]
