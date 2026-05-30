# User Guide: Upstream Proxy Protocol for Cilium Gateway API

## Overview

This feature enables Cilium Gateway API to forward the real client IP to backend
services using PROXY protocol on upstream (backend) connections. It applies to
**TLSPassthrough routes only** (`TLSRoute`), and is controlled per-backend via a
Service label.

**Without this feature**: Backend sees the Envoy Pod IP as the source address.
**With this feature**: Backend receives a PROXY protocol header containing the
real client IP before the TLS `ClientHello`.

## Prerequisites

- Cilium v1.18.10+ (this fork, branch `feature/upstream-proxy-protocol`)
- Backend Services must support PROXY protocol before TLS (see [Backend Requirements](#backend-requirements))
- Kubernetes Gateway API CRDs installed

## Service Label

Enable PROXY protocol for a specific backend by adding a label to its Kubernetes
Service:

| Label key | Valid values | Notes |
|---|---|---|
| `service.cilium.io/proxy-protocol` | `"v1"` | Text format (human-readable, useful for debugging) |
| `service.cilium.io/proxy-protocol` | `"v2"` | Binary format (recommended for production) |

Values are **case-sensitive and exact**. Labels set to `"true"`, `"V1"`, `"V2"`,
`"enabled"`, or anything other than the two values above are silently ignored and
PROXY protocol is not enabled for that backend.

## Quick Start

### Step 1: Deploy a TLSPassthrough route

The feature only works with `TLSRoute` (TLSPassthrough). Set up a Gateway listener
and a `TLSRoute` pointing to your backend:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: my-gateway
  namespace: default
spec:
  gatewayClassName: cilium
  listeners:
    - name: tls-passthrough
      port: 443
      protocol: TLS
      tls:
        mode: Passthrough
---
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: TLSRoute
metadata:
  name: my-tls-route
  namespace: default
spec:
  parentRefs:
    - name: my-gateway
      sectionName: tls-passthrough
  rules:
    - backendRefs:
        - name: my-backend
          port: 8443
```

### Step 2: Label the backend Service

Add the `service.cilium.io/proxy-protocol` label to your backend Service:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: my-backend
  namespace: default
  labels:
    service.cilium.io/proxy-protocol: "v2"
spec:
  selector:
    app: my-backend
  ports:
    - port: 8443
      targetPort: 8443
```

No changes to the Gateway or TLSRoute are needed. The label on the Service is all
that's required.

### Step 3: Configure your backend to accept PROXY protocol

See [Backend Requirements](#backend-requirements) below.

### Step 4: Verify

Check the generated `CiliumEnvoyConfig` to confirm the transport socket is set for
your cluster:

```bash
kubectl get ciliumenvoyconfig -n default -o json | \
  jq '.items[].spec.resources[] | select(.["@type"] | contains("Cluster")) | .transport_socket'
```

Expected output for a `v2` label:

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

If the field is absent, PROXY protocol is not active for that cluster (check
[Troubleshooting](#troubleshooting)).

## Configuration Reference

| Configuration | Value | Description |
|---|---|---|
| Service label key | `service.cilium.io/proxy-protocol` | Enables upstream PROXY protocol for this backend |
| Label value `"v1"` | Text format | `PROXY TCP4 <src> <dst> <sport> <dport>\r\n` |
| Label value `"v2"` | Binary format | Compact binary header, recommended |
| `gatewayAPI.enableProxyProtocol` (Helm) | `true`/`false` | Separate flag: enables PROXY protocol *parsing* on the listener (downstream). Independent of the upstream label. |

> **Note**: The listener-side `enableProxyProtocol` and the upstream Service label
> are independent. You can use either or both:
> - Listener flag only: Cilium parses PROXY headers from your cloud LB but does not forward to backends.
> - Service label only: Cilium forwards the real IP to labeled backends (using the TCP connection source as seen by Envoy).
> - Both: Full end-to-end chain from cloud LB through Cilium to backend.

## Backend Requirements

Your backend must accept a PROXY protocol header *before* the TLS `ClientHello`.
This is different from backends that accept PROXY protocol before plain HTTP — here
the PROXY header arrives before the TLS handshake begins.

### HAProxy

HAProxy's `accept-proxy` option on a TLS bind reads the PROXY header first, then
performs the TLS handshake. This is the primary use case for this feature.

```haproxy
frontend my-frontend
    bind *:8443 ssl crt /etc/ssl/my-cert.pem accept-proxy
    # $src will be the real client IP from the PROXY header
```

### nginx (stream module)

For raw TCP/TLS proxying with the stream module:

```nginx
stream {
    server {
        listen 8443 ssl proxy_protocol;
        ssl_certificate /etc/ssl/my-cert.pem;
        ssl_certificate_key /etc/ssl/my-key.pem;
        # $proxy_protocol_addr is the real client IP
    }
}
```

### Custom Go application

```go
import "github.com/pires/go-proxyproto"

listener, _ := net.Listen("tcp", ":8443")
proxyListener := &proxyproto.Listener{Listener: listener}
conn, _ := proxyListener.Accept()
// conn now exposes the PROXY header; wrap with TLS next
tlsConn := tls.Server(conn, tlsConfig)
header := conn.(*proxyproto.Conn).ProxyHeader()
realClientIP := header.SourceAddr.(*net.TCPAddr).IP
```

## Limitations

1. **TLSPassthrough routes only.** HTTP and HTTPS terminating routes are not
   supported. The feature is intentionally scoped to `TLSRoute` with TLSPassthrough
   mode. HTTP backends should use `X-Forwarded-For` headers instead.

2. **Per-backend label required.** There is no global flag. Each backend Service must
   be labeled individually. Backends without the label are unaffected.

3. **V1 and V2 both supported.** Choose based on what your backend accepts. V2 is
   binary and more compact; V1 is text and easier to inspect with `tcpdump`.

4. **Backend must understand PROXY protocol before TLS.** Not all TLS servers
   support this ordering. Verify your backend software explicitly documents support
   for PROXY protocol on TLS listeners (HAProxy does; plain nginx `http` blocks
   don't).

## Troubleshooting

### Label is present but PROXY protocol is not active

1. Confirm the label value is **exactly** `"v1"` or `"v2"` (lowercase, no quotes in
   the YAML value field):
   ```bash
   kubectl get service my-backend -n default -o jsonpath='{.metadata.labels.service\.cilium\.io/proxy-protocol}'
   ```
   If the output is empty or not `v1`/`v2`, the label is missing or incorrect.

2. Check that the route type is `TLSRoute` with TLSPassthrough mode. The feature
   does not apply to `HTTPRoute` or TLS termination at the gateway.

3. Restart the Cilium operator to force a reconcile:
   ```bash
   kubectl -n kube-system rollout restart deployment/cilium-operator
   ```

4. Check operator logs for label parsing:
   ```bash
   kubectl -n kube-system logs deployment/cilium-operator | grep proxy-protocol
   ```

### Backend receives unexpected binary data at connection start

The backend is not configured to accept PROXY protocol before TLS. Enable PROXY
protocol support on the backend (see [Backend Requirements](#backend-requirements)).
For HAProxy, make sure `accept-proxy` is on the `bind` line with the `ssl` keyword.

### Connection reset immediately after TCP handshake

This usually means the backend receives the PROXY header but doesn't parse it,
treating it as the start of a TLS `ClientHello`. Verify the backend is configured
to read the PROXY header before starting the TLS handshake.

### `CiliumEnvoyConfig` does not have `transport_socket`

1. Confirm the Service label is set correctly (see above).
2. Confirm the route is a `TLSRoute` with `tls.mode: Passthrough`.
3. Check for operator errors:
   ```bash
   kubectl -n kube-system logs deployment/cilium-operator | grep -i "envoy\|cluster\|proxy"
   ```

## Operational Notes

### Changing the label on a live cluster

When you add, change, or remove the `service.cilium.io/proxy-protocol` label, the
Cilium operator reconciles and updates the `CiliumEnvoyConfig`. Envoy receives
updated CDS (Cluster Discovery Service) resources and drains existing connections.
New connections will use the updated transport socket. Expect brief disruption for
active connections during the transition.

### Combining with downstream proxy protocol

If `enableProxyProtocol` (listener-side) is also enabled, the full chain is:

```
Client → [Cloud LB] ──(PROXY v2)──> Envoy
                                       │  parses real IP from LB's PROXY header
                                       │  uses real IP in outgoing PROXY header
                                       └──(PROXY v2 with real client IP + TLS ClientHello)──> Backend
```

This is the recommended setup when your cloud load balancer supports PROXY protocol
(e.g., AWS NLB with `proxy_protocol_v2.enabled: true`) and your backend is HAProxy
with `accept-proxy ssl`.
