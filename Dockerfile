FROM --platform=$BUILDPLATFORM golang:1.23-alpine as builder
# ARG TARGETARCH=arm64
# ARG TARGETOS=linux
ARG TARGETOS TARGETARCH

WORKDIR $GOPATH/src/getlantern/sing-box-extensions/
COPY . .

RUN apk add --no-cache build-base

RUN GOARCH=$TARGETARCH GOOS=$TARGETOS CGO_ENABLED=1 go build -v \
    -o /usr/local/bin/sing-box-extensions ./cmd/sing-box-extensions

FROM alpine

RUN apk --no-cache add ca-certificates tzdata nftables wireguard-tools

COPY --from=builder /usr/local/bin/sing-box-extensions /usr/local/bin/sing-box-extensions

ENTRYPOINT ["/usr/local/bin/sing-box-extensions", "-d", "/data", "-c", "/config.json", "run"]