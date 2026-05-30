# Technical Design: Upstream Proxy Protocol Support for Cilium Gateway API

## Background

Cilium Gateway API uses Envoy as the data plane. Traffic flows:

```
Client → [Cloud LB] → Envoy (Gateway) → Backend Pod
```

The **listener-side** proxy protocol (`gatewayAPI.enableProxyProtocol`) was already
supported: it configures Envoy to *receive* and *parse* PROXY protocol headers from
the upstream load balancer, extracting the real client IP into `X-Forwarded-For`.

The **upstream (cluster) side** was missing: Envoy did not *send* PROXY protocol
headers when connecting to backend services. Backend applications that need the real
client IP at the TCP connection layer (e.g., HAProxy, nginx with `proxy_protocol on`,
custom TCP servers) had no way to receive it.

### v1 Implementation and Why It Was Revised

The initial implementation (v1) added a global boolean flag
(`gatewayAPI.enableUpstreamProxyProtocol`) that applied PROXY protocol to **all**
HTTP clusters managed by the Gateway API controller. This was too broad: enabling it
for one backend would silently break every other backend that didn't expect a PROXY
header. It also hardcoded V2 and excluded TLSPassthrough routes entirely.

The v2 design replaces the global flag with a **per-backend Service label**. Each
backend opts in individually, and the feature scope is narrowed to TLSPassthrough
routes only, where the use case is most compelling (HAProxy terminating TLS after
reading the PROXY header).

## Design

### Dual-Condition Trigger

PROXY protocol is injected on an upstream connection when **both** conditions are met:

1. The route type is **TLSPassthrough** (`TLSRoute`).
2. The backend Service carries the label `service.cilium.io/proxy-protocol` with
   value `"v1"` or `"v2"` (exact lowercase).

Any other label value (e.g., `"true"`, `"V2"`, `"enabled"`) is silently ignored and
PROXY protocol is disabled for that backend.

### Data Flow (After Change)

```
Client → [Cloud LB] ──(TLS ClientHello)──> Envoy Listener (TLSPassthrough)
                                               │
                                               │  reads Service label
                                               │  service.cilium.io/proxy-protocol: "v2"
                                               │
                                               └──(PROXY v2 header + TLS ClientHello)──> Backend Pod
                                                                                           │
                                                                                           └── HAProxy: reads PROXY header, then handles TLS
```

The backend receives the PROXY header *before* the TLS `ClientHello`. HAProxy with
`bind ... ssl accept-proxy` does exactly this: it reads the PROXY header first, then
performs the TLS handshake.

### Version Selection

| Label value | Wire format | Notes |
|---|---|---|
| `"v1"` | Text (`PROXY TCP4 ...`) | Human-readable, useful for debugging |
| `"v2"` | Binary | More compact, recommended for production |

### Envoy Configuration Generated

When a TLSPassthrough cluster has the label set, the TCP cluster in the
`CiliumEnvoyConfig` resource gets a `transport_socket` field wrapping the raw buffer:

```json
{
  "name": "envoy.transport_sockets.proxy_protocol",
  "typed_config": {
    "@type": "type.googleapis.com/envoy.extensions.transport_sockets.proxy_protocol.v3.ProxyProtocolUpstreamTransport",
    "config": {
      "version": "V2"
    },
    "transport_socket": {
      "name": "envoy.transport_sockets.raw_buffer"
    }
  }
}
```

For `"v1"` the `version` field is `"V1"`. For backends without the label, no
`transport_socket` is set and the cluster behaves as before.

### Code Changes

| File | Change |
|------|--------|
| `operator/pkg/model/model.go` | Added `UpstreamProxyProtocol string` field to `model.Backend` |
| `operator/pkg/model/translation/envoy_cluster_mutator.go` | Added `withUpstreamProxyProtocol(version string)` ClusterMutator |
| `operator/pkg/model/translation/envoy_cluster.go` | Added `getUpstreamProxyProtocolVersion()`, `toProxyProtocolVersion()` helpers; wired `withUpstreamProxyProtocol()` in `tcpClusterMutators()` |
| `operator/pkg/gateway-api/translator.go` | Reads `service.cilium.io/proxy-protocol` label from Service; populates `Backend.UpstreamProxyProtocol` |

The global flag (`EnableGatewayAPIUpstreamProxyProtocol`), its Helm value, and its
ConfigMap entry are all removed.

### Scope Decisions

**HTTP routes are excluded.** HTTP clusters carry application-layer data. Inserting a
PROXY header at the TCP layer before an HTTP request would corrupt the HTTP stream
unless the backend explicitly expects it. The per-backend label approach could
theoretically support HTTP in the future, but the risk of misconfiguration is high
and the use case is better served by `X-Forwarded-For` headers at the HTTP layer.

**TLSPassthrough + PROXY protocol is valid.** HAProxy supports `bind ... ssl accept-proxy`,
which means it reads the PROXY header *before* the TLS `ClientHello`. The wire order is:

```
[PROXY v1/v2 header] → [TLS ClientHello] → [TLS handshake continues]
```

HAProxy parses the PROXY header, records the real client IP, then proceeds with the
TLS handshake as normal. This is a well-defined and documented HAProxy feature.

## Vendor Dependencies

All required Envoy proto types are already vendored:
- `vendor/github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/proxy_protocol/v3/upstream_proxy_protocol.pb.go`
- `vendor/github.com/envoyproxy/go-control-plane/envoy/config/core/v3/proxy_protocol.pb.go`

No new vendor dependencies are required.

## Testing

Unit tests added to `operator/pkg/model/translation/envoy_cluster_test.go`:
- `Test_withUpstreamProxyProtocol` — nil safety, V1/V2 version selection, raw_buffer fallback, existing socket wrapping
- `Test_tcpCluster_withUpstreamProxyProtocol_v1` — integration: full proto round-trip with V1
- `Test_tcpCluster_withUpstreamProxyProtocol_v2` — integration: full proto round-trip with V2
- `Test_tcpCluster_withUpstreamProxyProtocol_disabled` — no label produces no TransportSocket
- `Test_tcpCluster_withUpstreamProxyProtocol_invalidLabel` — invalid label value produces no TransportSocket
