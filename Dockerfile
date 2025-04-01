# CONTAINER FOR BUILDING BINARY
FROM --platform=${BUILDPLATFORM} golang:1.23.7 AS build

WORKDIR /app

# INSTALL DEPENDENCIES
COPY go.mod go.sum ./
RUN go mod download

# BUILD BINARY
COPY . .
RUN make build-aggkit build-tools

# CONTAINER FOR RUNNING BINARY
FROM --platform=${BUILDPLATFORM} debian:bookworm-slim
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
    ca-certificates \
    sqlite3 \
    procps \
    libssl-dev && \
    rm -rf /var/lib/apt/lists/*
COPY --from=build /app/target/aggkit /usr/local/bin/

# ADD NON-ROOT USER
RUN addgroup --system appgroup && adduser --system --ingroup appgroup appuser
USER appuser

EXPOSE 5576/tcp

ENTRYPOINT ["/usr/local/bin/aggkit"]
