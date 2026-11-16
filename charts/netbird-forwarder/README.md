# netbird-forwarder Helm chart

Deploys [netbird-forwarder](../../README.md). Each replica is a NetBird peer.

## Install

```shell
helm install db-mesh ./charts/netbird-forwarder -n mesh --create-namespace -f values.yaml
```

### Virtual addresses (routing peer mode)

All replicas serve all VIPs. Peers fail over between the replicas.

```yaml
netbird:
  managementURL: https://netbird.example.com
  setupKey:
    existingSecret: forwarder-setup-key
routes:
  - vip: 10.250.0.10
    port: 5432
    target: postgres.db.svc.cluster.local:5432
  - vip: 10.250.0.11
    port: 443
    target: grafana.monitoring.svc.cluster.local:3000
    proxyProtocol: true
```

Give the setup key an auto-group. In NetBird, use that group as `peer_groups` of the Network's router. The [main README](../../README.md#virtual-addresses-routing-peer-mode) describes the NetBird setup.

### Single target

Each replica listens on its own NetBird address and forwards to one target.

```yaml
replicaCount: 1
netbird:
  setupKey:
    existingSecret: forwarder-setup-key
  extraDnsLabels: [shared-db]
listener:
  port: "3306"
  target: service-database:3306
```

## Values

| Key | Default | Description |
|---|---|---|
| `image.repository` | `ghcr.io/wouter0100/netbird-forwarder` | Image |
| `image.tag` | chart `appVersion` | Image tag |
| `replicaCount` | `2` | Number of peers |
| `netbird.managementURL` | `""` | Management URL. Empty uses the NetBird default. |
| `netbird.setupKey.existingSecret` | `""` | Secret with the setup key. Takes precedence over `value`. |
| `netbird.setupKey.existingSecretKey` | `setup-key` | Key in that Secret |
| `netbird.setupKey.value` | `""` | Setup key. The chart creates a Secret for it. |
| `netbird.extraDnsLabels` | `[]` | Extra DNS labels for each peer |
| `routes` | `[]` | VIPs: `vip`, `port`, `target`, optional `proxyProtocol` |
| `listener.port`, `listener.target` | `""` | Single target. Set both or neither. |
| `listener.proxyProtocol` | `false` | PROXY protocol for the single target |
| `health.port` | `8081` | Pod-local `/healthz` port |
| `health.watchdogInterval`, `health.watchdogGrace` | `30s`, `3m` | Watchdog tuning |
| `resources` | 64Mi request, 128Mi limit | See the note below |
| `podDisruptionBudget.enabled` | `true` | PDB with `maxUnavailable: 1` when `replicaCount` is above 1 |
| `topologySpread.enabled` | `true` | Spread replicas over nodes when possible |

The chart needs one of `routes` or `listener`, and one of the setup key settings. It stops with an error otherwise.

The pod's memory cgroup pays for the page cache of the binary (about 40 MB) on top of the Go heap. Do not set the memory limit below 128Mi. With 64Mi, the kernel evicts the program's own pages and the pod restarts in a loop.

The pod runs as UID 65534 with a read-only root filesystem, without capabilities and without a service account token.
