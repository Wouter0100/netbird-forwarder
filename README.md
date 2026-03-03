# NetBird Forwarder

A very simple forwarder implementing a feature like TS_DEST_IP in NetBird.

## Environment Variables
- `NB_SETUP_KEY`: NetBird Setup Key, should be reusable and ephemeral.
- `NB_MANAGEMENT_URL`: NetBird management URL, if not the default.
- `NB_EXTRA_DNS_LABELS`: Extra DNS labels to add to the peer.
- `PROXY_LISTEN_PORT`: Port to listen on in the NetBird network on the peer's IP.
- `PROXY_TARGET_ADDR`: Target address to forward to, can be a hostname or IP. Should include a port.
- `PROXY_USE_PROXY_PROTOCOL`: Whether to use the PROXY protocol.

## Container image
A container image is available in this repository.