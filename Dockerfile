FROM --platform=$BUILDPLATFORM golang:1.24-bullseye as builder
ARG TARGETOS TARGETARCH
ARG GOPROXY=""

RUN set -ex \
    && apt-get update \
	 && apt-get install -y gcc-aarch64-linux-gnu g++-aarch64-linux-gnu qemu-user \
    && rm -rf /var/lib/apt/lists/*

WORKDIR $GOPATH/src/getlantern/sing-box-extensions/
COPY . .

ENV CC=aarch64-linux-gnu-gcc
ENV CXX=aarch64-linux-gnu-g++
ENV CGO_ENABLED=1
ENV GOOS=$TARGETOS
ENV GOARCH=$TARGETARCH  
RUN set -ex \
    && go build -v -tags \
       "with_gvisor,with_quic,with_dhcp,with_wireguard,with_ech,with_utls,with_reality_server,with_acme,with_clash_api" \
       -o /usr/local/bin/sing-box-extensions ./cmd/sing-box-extensions

FROM debian:bullseye-slim
RUN set -ex \
    && apt-get update \
    && apt-get install -y ca-certificates tzdata nftables wireguard-tools \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /usr/local/bin/sing-box-extensions /usr/local/bin/sing-box-extensions

ENTRYPOINT ["/usr/local/bin/sing-box-extensions", "-d", "/data", "-c", "/config.json", "run"]
