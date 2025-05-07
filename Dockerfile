FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder
COPY . /go/src/github.com/getlantern/sing-box-extensions
WORKDIR /go/src/github.com/getlantern/sing-box-extensions
ENV CGO_ENABLED=1
RUN set -ex \
    && apk add git build-base \
    && go build -v -trimpath -tags \
        "with_gvisor,with_quic,with_dhcp,with_wireguard,with_ech,with_utls,with_reality_server,with_clash_api" \
        -o /go/bin/sing-box \
        -ldflags "-s -w -buildid=" \
        ./cmd/sing-box-extensions
FROM alpine AS dist
RUN set -ex \
    && apk upgrade \
    && apk add bash tzdata ca-certificates nftables \
    && rm -rf /var/cache/apk/*
COPY --from=builder /go/bin/sing-box /usr/local/bin/sing-box-extensions
ENTRYPOINT ["sing-box-extensions"]
