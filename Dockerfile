FROM golang:1.26-alpine AS builder

# Own version, reported to NetBird as semver build metadata. CI passes the git tag.
ARG FORWARDER_VERSION=dev

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Stamp the version the peer reports to Management, which is what the NetBird
# dashboard shows. Unstamped builds report "development".
#
# The embedded NetBird release has to be the semver core: Management gates
# firewall port ranges (>= 0.48.0), native SSH rules (>= 0.60.0) and remote jobs
# (>= 0.64.0) on this string, and anything whose leading component is not the
# NetBird version fails those >= checks, silently in the firewall case. So the
# forwarder's own version rides along as build metadata, which comparisons
# ignore. Read from go.mod so it cannot drift from the dependency.
RUN NETBIRD_VERSION="$(go list -m -f '{{.Version}}' github.com/netbirdio/netbird | sed 's/^v//')" && \
    FWD_VERSION="$(echo "${FORWARDER_VERSION}" | sed 's/^v//')" && \
    CGO_ENABLED=0 GOOS=linux go build \
      -ldflags "-s -w -X github.com/netbirdio/netbird/version.version=${NETBIRD_VERSION}+forwarder.${FWD_VERSION}" \
      -o /app/netbird-forwarder .

FROM alpine:latest

RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/netbird-forwarder .

CMD ["./netbird-forwarder"]
