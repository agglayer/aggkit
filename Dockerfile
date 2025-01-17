# CONTAINER FOR BUILDING BINARY
FROM --platform=${BUILDPLATFORM} golang:1.22.4 AS build

WORKDIR $GOPATH/src/github.com/agglayer/aggkit

# INSTALL DEPENDENCIES
COPY go.mod go.sum ./
RUN go mod download

# BUILD BINARY
COPY . .
RUN make build-go build-tools

# CONTAINER FOR RUNNING BINARY
FROM --platform=${BUILDPLATFORM} debian:bookworm-slim

RUN apt-get update && apt-get install -y ca-certificates sqlite3 procps libssl-dev && rm -rf /var/lib/apt/lists/*
COPY --from=build /go/src/github.com/agglayer/aggkit/target/aggkit-node /usr/local/bin/

EXPOSE 5576/tcp

CMD ["/bin/sh", "-c", "aggkit"]
