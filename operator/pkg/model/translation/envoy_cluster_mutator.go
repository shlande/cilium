// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package translation

import (
	envoy_config_cluster_v3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	envoy_config_core_v3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	envoy_transport_sockets_proxy_protocol_v3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/proxy_protocol/v3"
	raw_bufferv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/raw_buffer/v3"
	envoy_upstreams_http_v3 "github.com/envoyproxy/go-control-plane/envoy/extensions/upstreams/http/v3"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
)

type ClusterMutator func(*envoy_config_cluster_v3.Cluster) *envoy_config_cluster_v3.Cluster

// withClusterLbPolicy sets the cluster's load balancing policy.
// https://www.envoyproxy.io/docs/envoy/latest/intro/arch_overview/upstream/load_balancing/load_balancers
func withClusterLbPolicy(lbPolicy int32) ClusterMutator {
	return func(cluster *envoy_config_cluster_v3.Cluster) *envoy_config_cluster_v3.Cluster {
		if cluster == nil {
			return cluster
		}
		cluster.LbPolicy = envoy_config_cluster_v3.Cluster_LbPolicy(lbPolicy)
		return cluster
	}
}

// withOutlierDetection enables outlier detection on the cluster.
func withOutlierDetection(splitExternalLocalOriginErrors bool) ClusterMutator {
	return func(cluster *envoy_config_cluster_v3.Cluster) *envoy_config_cluster_v3.Cluster {
		if cluster == nil {
			return cluster
		}
		cluster.OutlierDetection = &envoy_config_cluster_v3.OutlierDetection{
			SplitExternalLocalOriginErrors: splitExternalLocalOriginErrors,
		}
		return cluster
	}
}

// withConnectionTimeout sets the cluster's connection timeout.
func withConnectionTimeout(seconds int) ClusterMutator {
	return func(cluster *envoy_config_cluster_v3.Cluster) *envoy_config_cluster_v3.Cluster {
		if cluster == nil {
			return cluster
		}
		cluster.ConnectTimeout = &durationpb.Duration{Seconds: int64(seconds)}
		return cluster
	}
}

// withIdleTimeout sets the cluster's connection idle timeout.
func withIdleTimeout(seconds int) ClusterMutator {
	return func(cluster *envoy_config_cluster_v3.Cluster) *envoy_config_cluster_v3.Cluster {
		if cluster == nil {
			return cluster
		}
		a := cluster.TypedExtensionProtocolOptions[httpProtocolOptionsType]
		opts := &envoy_upstreams_http_v3.HttpProtocolOptions{}
		if err := a.UnmarshalTo(opts); err != nil {
			return cluster
		}
		opts.CommonHttpProtocolOptions = &envoy_config_core_v3.HttpProtocolOptions{
			IdleTimeout: &durationpb.Duration{Seconds: int64(seconds)},
		}
		cluster.TypedExtensionProtocolOptions[httpProtocolOptionsType] = toAny(opts)
		return cluster
	}
}

func withProtocol(protocolVersion HTTPVersionType) ClusterMutator {
	return func(cluster *envoy_config_cluster_v3.Cluster) *envoy_config_cluster_v3.Cluster {
		a := cluster.TypedExtensionProtocolOptions[httpProtocolOptionsType]
		options := &envoy_upstreams_http_v3.HttpProtocolOptions{}
		if err := a.UnmarshalTo(options); err != nil {
			return cluster
		}
		switch protocolVersion {
		// Default protocol version in Envoy is HTTP1.1.
		case HTTPVersion1, HTTPVersionAuto:
			options.UpstreamProtocolOptions = &envoy_upstreams_http_v3.HttpProtocolOptions_ExplicitHttpConfig_{
				ExplicitHttpConfig: &envoy_upstreams_http_v3.HttpProtocolOptions_ExplicitHttpConfig{
					ProtocolConfig: &envoy_upstreams_http_v3.HttpProtocolOptions_ExplicitHttpConfig_HttpProtocolOptions{},
				},
			}
		case HTTPVersion2:
			options.UpstreamProtocolOptions = &envoy_upstreams_http_v3.HttpProtocolOptions_ExplicitHttpConfig_{
				ExplicitHttpConfig: &envoy_upstreams_http_v3.HttpProtocolOptions_ExplicitHttpConfig{
					ProtocolConfig: &envoy_upstreams_http_v3.HttpProtocolOptions_ExplicitHttpConfig_Http2ProtocolOptions{},
				},
			}
		case HTTPVersion3:
			options.UpstreamProtocolOptions = &envoy_upstreams_http_v3.HttpProtocolOptions_ExplicitHttpConfig_{
				ExplicitHttpConfig: &envoy_upstreams_http_v3.HttpProtocolOptions_ExplicitHttpConfig{
					ProtocolConfig: &envoy_upstreams_http_v3.HttpProtocolOptions_ExplicitHttpConfig_Http3ProtocolOptions{},
				},
			}
		}

		cluster.TypedExtensionProtocolOptions = map[string]*anypb.Any{
			httpProtocolOptionsType: toAny(options),
		}
		return cluster
	}
}

// withUpstreamProxyProtocol wraps the cluster's upstream transport socket with
// Envoy's ProxyProtocolUpstreamTransport so that PROXY protocol headers are
// sent to backend services, forwarding the real client IP.
//
// The version parameter controls which PROXY protocol format is used:
//   - envoy_config_core_v3.ProxyProtocolConfig_V1: human-readable text format
//   - envoy_config_core_v3.ProxyProtocolConfig_V2: binary format (recommended)
//
// If the cluster has no existing TransportSocket, a raw_buffer inner socket is
// used as the fallback (the default Envoy behavior for plain TCP).
//
// This mutator is applied to TLSPassthrough clusters when the backend Service
// has the label "service.cilium.io/proxy-protocol: v1" or "v2". In this case,
// Envoy sends the PROXY header before the raw TCP bytes (which are a TLS
// ClientHello). This is valid for backends configured with accept-proxy + ssl
// (e.g., HAProxy "bind *:443 ssl accept-proxy", nginx "listen 443 ssl proxy_protocol").
//
// This mutator is NOT applied to HTTP/HTTPS clusters (clusterMutators).
func withUpstreamProxyProtocol(version envoy_config_core_v3.ProxyProtocolConfig_Version) ClusterMutator {
	return func(cluster *envoy_config_cluster_v3.Cluster) *envoy_config_cluster_v3.Cluster {
		if cluster == nil {
			return cluster
		}
		innerSocket := cluster.TransportSocket
		if innerSocket == nil {
			innerSocket = &envoy_config_core_v3.TransportSocket{
				Name: rawBufferTransportSocketName,
				ConfigType: &envoy_config_core_v3.TransportSocket_TypedConfig{
					TypedConfig: toAny(&raw_bufferv3.RawBuffer{}),
				},
			}
		}
		cluster.TransportSocket = &envoy_config_core_v3.TransportSocket{
			Name: proxyProtocolTransportSocketName,
			ConfigType: &envoy_config_core_v3.TransportSocket_TypedConfig{
				TypedConfig: toAny(&envoy_transport_sockets_proxy_protocol_v3.ProxyProtocolUpstreamTransport{
					Config: &envoy_config_core_v3.ProxyProtocolConfig{
						Version: version,
					},
					TransportSocket: innerSocket,
				}),
			},
		}
		return cluster
	}
}
