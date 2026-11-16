# NetBird Forwarder

A very simple forwarder implementing a feature like TS_DEST_IP in NetBird.

## Deploy

Use the Helm chart in [`charts/netbird-forwarder`](charts/netbird-forwarder/README.md).

## Environment Variables
- `NB_SETUP_KEY`: NetBird Setup Key, should be reusable and ephemeral.
- `NB_MANAGEMENT_URL`: NetBird management URL, if not the default.
- `NB_EXTRA_DNS_LABELS`: Extra DNS labels to add to the peer.
- `PROXY_LISTEN_PORT`: Port to listen on in the NetBird network on the peer's IP.
- `PROXY_TARGET_ADDR`: Target address to forward to, can be a hostname or IP. Should include a port.
- `PROXY_USE_PROXY_PROTOCOL`: Whether to use the PROXY protocol.
- `PROXY_ROUTES_FILE`: Path to a route table. The forwarder then acts as a routing
  peer for virtual addresses (see below). `PROXY_LISTEN_PORT` and
  `PROXY_TARGET_ADDR` become optional, but must be set together.

### Virtual addresses (routing peer mode)

With `PROXY_ROUTES_FILE`, the forwarder serves one or more virtual addresses
(VIPs). Peers dial a VIP, NetBird routes it to one of the forwarder replicas, and
the forwarder connects to the target of that VIP. All replicas serve all VIPs, so
peers fail over to another replica when one stops.

```yaml
routes:
  - vip: 10.250.0.10
    port: 5432
    target: postgres.db.svc.cluster.local:5432
  - vip: 10.250.0.11
    port: 443
    target: grafana.monitoring.svc.cluster.local:3000
    proxyProtocol: true
```

- The forwarder serves only the listed VIP and port pairs. It refuses all other
  routed connections, whatever the NetBird policies allow.
- `proxyProtocol` sends a PROXY protocol v1 header with the peer's NetBird
  address as the source and the VIP as the destination.
- The forwarder routes only TCP.
- The forwarder does not need `NET_ADMIN`. The routed traffic stays in the
  userspace network stack.

Set up NetBird once per deployment:

1. Give the setup key an auto-group, for example `forwarders`.
2. Create a Network. Add each VIP as a resource (`/32`), each in its own group.
3. Add a router to the Network with `peer_groups: [forwarders]` and masquerade on.
4. Add one policy per VIP group, from the peers that need access.
5. Optional: add a DNS record that points a name to each VIP.

Use VIPs from a range that no site and no client uses. A change to the route
table needs a restart. On SIGTERM the forwarder deregisters, so peers switch to
another replica at once.

This mode needs `embed.Options.EnableServerRoutes` and `RoutedDialer`, which are
not in a NetBird release yet. `go.mod` replaces NetBird with a fork until they are.

### Health and self-healing
The process exposes an HTTP liveness endpoint and runs an internal watchdog so a
wedged peer is restarted instead of silently failing to forward traffic. Wire the
endpoint to a Kubernetes `livenessProbe`.

- `HEALTH_LISTEN_PORT`: Pod-local port for the `/healthz` endpoint (default `8081`).
  Not exposed on the NetBird network.
- `WATCHDOG_INTERVAL`: How often health is re-evaluated (default `30s`).
- `WATCHDOG_GRACE`: How long the peer must be continuously unhealthy before
  `/healthz` returns 500 and the watchdog exits the process to trigger a pod
  recreation (default `3m`). This hysteresis absorbs transient management blips.

Every 30s the watchdog also dials its own proxy port over the NetBird network
and expects to be accepted. This is the check that needs no remote device to be
awake, and it tests the wedge directly: `Dial` resolves the engine's current
netstack while the listener stays bound to the netstack it was created on, so
once the engine rebuilds its net the listener is orphaned and clients are refused
on a port this process still believes it is serving. A listener that has quietly
closed fails the same probe. The probe is served by the accept loop and never
reaches the target, and it is used only after it has succeeded once at startup;
if it cannot, that is logged and liveness carries on without it.

Health is considered failing only on unambiguous conditions, and only on
conditions about this peer itself: the client status call errors, management and
signal are both disconnected, the self-probe is refused, or the accept loop is
persistently failing. Remote
peer state is deliberately not consulted. A device reported as connected may sit
for hours with no WireGuard handshake, because with lazy connections that is what
an idle or sleeping laptop looks like, and recreating this pod cannot repair
somebody else's tunnel anyway.

If the embedded NetBird engine stops for good (e.g. the peer was deregistered
and NetBird gives up its connection retry loop), that is not recoverable in
this process, so the watchdog exits immediately (after a brief re-check) rather
than waiting out `WATCHDOG_GRACE`.

On graceful shutdown the peer deregisters itself from Management (the peer's own
`Logout` RPC, which deletes it) so its DNS label and routes are freed at once,
instead of lingering until Management's ~10 minute ephemeral offline cleanup.
This is best-effort with a short timeout and requires a reusable setup key; on
failure it falls back to that cleanup.

## Container image
A container image is available in this repository.