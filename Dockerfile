FROM alpine:edge

# Set the timezone and install CA certificates
RUN apk --no-cache add ca-certificates tzdata nftables wireguard-tools

COPY cmd/sing-box-extensions /sing-box-extensions

# Set the entrypoint command
ENTRYPOINT ["/sing-box-extensions", "-d", "/data", "-c", "/config.json", "run"]