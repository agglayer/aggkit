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

# Compile binary with CGO enabled
RUN make build-aggkit

# ================================
# STAGE 2: Final runtime image
# ================================
FROM alpine:3.22

# Install runtime dependencies
RUN apk add --no-cache sqlite-libs ca-certificates

# Add non-root user with home directory (required for AWS credentials)
RUN addgroup appgroup && \
    adduser -D -G appgroup -h /home/appuser appuser && \
    mkdir -p /home/appuser && \
    chown -R appuser:appgroup /home/appuser

# Set working directory to home
WORKDIR /home/appuser
USER appuser

# Copy built binary
COPY --from=builder /app/target/aggkit /usr/local/bin/aggkit

EXPOSE 5576/tcp

ENTRYPOINT ["/usr/local/bin/aggkit"]
