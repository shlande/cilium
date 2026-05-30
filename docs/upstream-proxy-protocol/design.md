# Technical Design: Upstream Proxy Protocol Support for Cilium Gateway API

## Background

Cilium Gateway API uses Envoy as the data plane. Traffic flows:

```
Client → [Cloud LB] → Envoy (Gateway) → Backend Pod
```

The **listener-side** proxy protocol (`gatewayAPI.enableProxyProtocol`) was already
supported: it configures Envoy to *receive* and *parse* PROXY protocol headers from
the upstream load balancer, extracting the real client IP into `X-Forwarded-For`.

However, the **upstream (cluster) side** was missing: Envoy did not *send* PROXY
protocol headers when connecting to backend services. Backend applications that
need the real client IP at the TCP connection layer (e.g., HAProxy, nginx with
`proxy_protocol on`, custom TCP servers) had no way to receive it.

## Design

### What Changed

A new `ClusterMutator` — `withUpstreamProxyProtocol()` — wraps the Envoy cluster's
upstream `TransportSocket` with `ProxyProtocolUpstreamTransport`. This causes Envoy
to prepend a PROXY protocol v2 header to every new upstream TCP connection.

### Data Flow (After Change)

```
Client → [Cloud LB] ──(PROXY v2)──> Envoy Listener
                                       │  (parses real IP)
                                       │
                                       └──(PROXY v2 header + data)──> Backend Pod
                                                                        │
                                                                        └── reads real client IP from PROXY header
```

### Envoy Configuration Generated

When `UseUpstreamProxyProtocol: true`, each HTTP cluster in the `CiliumEnvoyConfig`
resource will have a `transport_socket` field:

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

### Code Changes

| File | Change |
|------|--------|
| `operator/pkg/model/translation/cec_translator.go` | Added `UseUpstreamProxyProtocol bool` to `ClusterConfig` |
| `operator/pkg/model/translation/envoy_cluster_mutator.go` | Added `withUpstreamProxyProtocol()` ClusterMutator |
| `operator/pkg/model/translation/envoy_cluster.go` | Added `rawBufferTransportSocketName`, `proxyProtocolTransportSocketName` constants; wired `withUpstreamProxyProtocol()` in `clusterMutators()` |
| `operator/pkg/gateway-api/cell.go` | Added `EnableGatewayAPIUpstreamProxyProtocol` flag and wiring |
| `install/kubernetes/cilium/values.yaml` | Added `gatewayAPI.enableUpstreamProxyProtocol: false` |
| `install/kubernetes/cilium/values.schema.json` | Added schema entry |
| `install/kubernetes/cilium/templates/cilium-configmap.yaml` | Added `enable-gateway-api-upstream-proxy-protocol` mapping |

### Scope Decisions

**`tcpClusterMutators()` is intentionally excluded.** TLSPassthrough routes (`TLSRoute`)
use `tcpCluster()` which passes raw TLS bytes to the backend. Inserting a PROXY protocol
header before the TLS `ClientHello` would corrupt the TLS handshake. If TLSPassthrough
+ PROXY protocol is needed in the future, it requires a separate, TLS-aware design.

**PROXY protocol version is hardcoded to V2.** V2 is the binary format and is more
efficient and widely supported than V1 (text format). Version configurability can be
added in a future iteration without breaking the existing API (by changing the bool
flag to a string enum).

## Vendor Dependencies

All required Envoy proto types are already vendored:
- `vendor/github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/proxy_protocol/v3/upstream_proxy_protocol.pb.go`
- `vendor/github.com/envoyproxy/go-control-plane/envoy/config/core/v3/proxy_protocol.pb.go`

No new vendor dependencies are required.

## Testing

Unit tests added to `operator/pkg/model/translation/envoy_cluster_test.go`:
- `Test_withUpstreamProxyProtocol` — nil safety, V2 version, raw_buffer fallback, existing socket wrapping
- `Test_httpCluster_withUpstreamProxyProtocol` — integration: full proto round-trip
- `Test_httpCluster_withUpstreamProxyProtocol_disabled` — feature disabled produces no TransportSocket
