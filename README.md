# NetBird Forwarder

A very simple forwarder implementing a feature like TS_DEST_IP in NetBird.

## Environment Variables
- `NB_SETUP_KEY`: NetBird Setup Key, should be reusable and ephemeral.
- `NB_MANAGEMENT_URL`: NetBird management URL, if not the default.
- `NB_EXTRA_DNS_LABELS`: Extra DNS labels to add to the peer.
- `PROXY_LISTEN_PORT`: Port to listen on in the NetBird network on the peer's IP.
- `PROXY_TARGET_ADDR`: Target address to forward to, can be a hostname or IP. Should include a port.
- `PROXY_USE_PROXY_PROTOCOL`: Whether to use the PROXY protocol.

### Health and self-healing
The process exposes an HTTP liveness endpoint and runs an internal watchdog so a
wedged peer is restarted instead of silently failing to forward traffic. Wire the
endpoint to a Kubernetes `livenessProbe`.

- `HEALTH_LISTEN_PORT`: Pod-local port for the `/healthz` endpoint (default `8081`).
  Not exposed on the NetBird network.
- `HEALTH_HANDSHAKE_STALE`: A peer reported as connected but with no WireGuard
  handshake within this window is treated as a dead tunnel (default `5m`, Go
  duration). Set `0` to disable this check. Peers that are simply offline are not
  connected, so they never trip it.
- `WATCHDOG_INTERVAL`: How often health is re-evaluated (default `30s`).
- `WATCHDOG_GRACE`: How long the peer must be continuously unhealthy before
  `/healthz` returns 500 and the watchdog exits the process to trigger a pod
  recreation (default `3m`). This hysteresis absorbs transient management blips.

Health is considered failing only on unambiguous conditions: the client status
call errors, management and signal are both disconnected, a connected peer's
WireGuard tunnel is stale, or the accept loop is persistently failing.

## Container image
A container image is available in this repository.