FROM --platform=$BUILDPLATFORM golang:1.23-bullseye as builder
ARG TARGETOS TARGETARCH

RUN apt-get update && apt-get install -y \
    gcc-aarch64-linux-gnu g++-aarch64-linux-gnu qemu-user \
    && rm -rf /var/lib/apt/lists/*

WORKDIR $GOPATH/src/getlantern/sing-box-extensions/
COPY . .

RUN CC=aarch64-linux-gnu-gcc CXX=aarch64-linux-gnu-g++ GOARCH=$TARGETARCH GOOS=$TARGETOS CGO_ENABLED=1 go build -v \
    -o /usr/local/bin/sing-box-extensions ./cmd/sing-box-extensions

FROM alpine

RUN apk --no-cache add ca-certificates tzdata nftables wireguard-tools

COPY --from=builder /usr/local/bin/sing-box-extensions /usr/local/bin/sing-box-extensions

ENTRYPOINT ["/usr/local/bin/sing-box-extensions", "-d", "/data", "-c", "/config.json", "run"]