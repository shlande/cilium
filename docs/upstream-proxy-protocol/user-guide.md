# User Guide: Upstream Proxy Protocol for Cilium Gateway API

## Overview

This feature enables Cilium Gateway API to forward the real client IP to backend
services using **PROXY protocol v2** on upstream (backend) connections.

**Without this feature**: Backend sees the Envoy Pod IP as the source address.
**With this feature**: Backend receives a PROXY protocol header containing the
real client IP before the first data byte.

## Prerequisites

- Cilium v1.18.10+ (this fork, branch `feature/upstream-proxy-protocol`)
- Backend services must support PROXY protocol v2 (see [Backend Requirements](#backend-requirements))
- Kubernetes Gateway API CRDs installed

## Quick Start

### Step 1: Enable the feature via Helm

```bash
helm upgrade cilium ./install/kubernetes/cilium \
  --namespace kube-system \
  --reuse-values \
  --set gatewayAPI.enabled=true \
  --set gatewayAPI.enableUpstreamProxyProtocol=true
```

Restart the operator to pick up the new configuration:

```bash
kubectl -n kube-system rollout restart deployment/cilium-operator
```

### Step 2: Configure your Gateway and HTTPRoute

No changes to Gateway or HTTPRoute resources are required. The feature applies
globally to all clusters managed by the Gateway API controller.

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: my-gateway
  namespace: default
spec:
  gatewayClassName: cilium
  listeners:
    - name: http
      port: 80
      protocol: HTTP
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: my-route
  namespace: default
spec:
  parentRefs:
    - name: my-gateway
  rules:
    - backendRefs:
        - name: my-backend
          port: 8080
```

### Step 3: Configure your backend to accept PROXY protocol

See [Backend Requirements](#backend-requirements) below.

### Step 4: Verify

Check the generated `CiliumEnvoyConfig` to confirm the transport socket is set:

```bash
kubectl get ciliumenvoyconfig -n default -o json | \
  jq '.items[].spec.resources[] | select(.["@type"] | contains("Cluster")) | .transport_socket'
```

Expected output:
```json
{
  "name": "envoy.transport_sockets.proxy_protocol",
  "typed_config": {
    "@type": "type.googleapis.com/envoy.extensions.transport_sockets.proxy_protocol.v3.ProxyProtocolUpstreamTransport",
    "config": { "version": "V2" },
    "transport_socket": { "name": "envoy.transport_sockets.raw_buffer" }
  }
}
```

## Configuration Reference

| Helm Value | Operator Flag | Default | Description |
|---|---|---|---|
| `gatewayAPI.enableUpstreamProxyProtocol` | `--enable-gateway-api-upstream-proxy-protocol` | `false` | Enable PROXY protocol v2 on upstream connections |
| `gatewayAPI.enableProxyProtocol` | `--enable-gateway-api-proxy-protocol` | `false` | Enable PROXY protocol parsing on the listener (downstream) |

> **Note**: These two flags are independent. You can enable either or both:
> - `enableProxyProtocol` only: Cilium parses PROXY protocol from your cloud LB but does NOT forward to backends
> - `enableUpstreamProxyProtocol` only: Cilium forwards real IP to backends (using the TCP connection source address as seen by Envoy)
> - Both enabled: Full end-to-end chain — cloud LB → Cilium (parses) → backend (receives PROXY header)

## Backend Requirements

Your backend must be configured to **accept** PROXY protocol v2 before the TCP payload.

### nginx

```nginx
server {
    listen 8080 proxy_protocol;
    # Use $proxy_protocol_addr for the real client IP
    set_real_ip_from 0.0.0.0/0;
    real_ip_header proxy_protocol;
}
```

### HAProxy

```haproxy
frontend my-frontend
    bind *:8080 accept-proxy
    # $src will be the real client IP
```

### Custom Go application

```go
import "github.com/pires/go-proxyproto"

listener, _ := net.Listen("tcp", ":8080")
proxyListener := &proxyproto.Listener{Listener: listener}
conn, _ := proxyListener.Accept()
header := conn.ProxyHeader()
realClientIP := header.SourceAddr.(*net.TCPAddr).IP
```

## Limitations

1. **TLSPassthrough routes are not supported.** The feature applies only to HTTP/HTTPS
   clusters. TLSPassthrough routes (`TLSRoute`) pass raw TLS bytes to the backend;
   inserting a PROXY protocol header would corrupt the TLS handshake.

2. **PROXY protocol version is fixed at V2.** V1 (text format) is not currently
   supported. If your backend only supports V1, it cannot be used with this feature.

3. **Global flag only.** The feature cannot be enabled per-route or per-backend.
   All backends behind the Gateway API controller will receive PROXY protocol headers
   when the flag is enabled.

4. **Backend must support PROXY protocol.** Backends that do not understand PROXY
   protocol will receive the binary header as unexpected data and likely fail.

## Troubleshooting

### Backend receives unexpected data at connection start

The backend is not configured to accept PROXY protocol. Enable PROXY protocol support
on the backend (see [Backend Requirements](#backend-requirements)).

### `CiliumEnvoyConfig` does not have `transport_socket`

1. Verify the flag is set: `kubectl -n kube-system get configmap cilium-config -o json | jq '.data["enable-gateway-api-upstream-proxy-protocol"]'`
2. Verify the operator has restarted after the config change
3. Check operator logs: `kubectl -n kube-system logs deployment/cilium-operator | grep upstream-proxy-protocol`

### Connection reset immediately after TCP handshake

This typically means the backend is receiving the PROXY protocol header but not
parsing it. Verify the backend configuration.

## Operational Notes

### Toggling the flag on a live cluster

When the flag is changed, the Cilium operator will update the `CiliumEnvoyConfig`
resources. Envoy will receive updated CDS (Cluster Discovery Service) resources and
drain existing connections gracefully. New connections will use the updated transport
socket configuration. Expect brief connection disruption during the transition.

### Combining with downstream proxy protocol

If both `enableProxyProtocol` (downstream) and `enableUpstreamProxyProtocol` (upstream)
are enabled, the full chain is:

```
Client → [Cloud LB] ──(PROXY v2)──> Envoy
                                       │  parses real IP from LB's PROXY header
                                       │  uses real IP in outgoing PROXY header
                                       └──(PROXY v2 with real client IP)──> Backend
```

This is the recommended configuration when using a cloud load balancer that supports
PROXY protocol (e.g., AWS NLB with `proxy_protocol_v2.enabled: true`).
