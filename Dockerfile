# ================================
# STAGE 1: Build binary
# ================================
FROM --platform=${BUILDPLATFORM} golang:1.24.4-alpine AS builder

# Install build dependencies and prepare workdir
RUN apk add --no-cache gcc make musl-dev sqlite-dev && \
    addgroup -S appgroup && adduser -S appuser -G appgroup && \
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
# STAGE 2: Final runtime image
# ================================
FROM alpine:3.20

# Install runtime dependencies and create user
RUN apk add --no-cache sqlite-libs ca-certificates && \
    addgroup -S appgroup && adduser -S appuser -G appgroup

USER appuser

# Copy built binary
COPY --from=builder /app/target/aggkit /usr/local/bin/aggkit

EXPOSE 5576/tcp

ENTRYPOINT ["/usr/local/bin/aggkit"]
