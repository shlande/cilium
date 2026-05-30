// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package translation

import (
	envoy_config_cluster_v3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	envoy_config_core_v3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	envoy_transport_sockets_proxy_protocol_v3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/proxy_protocol/v3"
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
// Envoy's ProxyProtocolUpstreamTransport so that PROXY protocol v2 headers are
// sent to backend services, forwarding the real client IP.
//
// If the cluster has no existing TransportSocket, a raw_buffer inner socket is
// used as the fallback (the default Envoy behavior for plain TCP).
//
// Note: this mutator is intentionally NOT applied to tcpClusterMutators()
// (TLSPassthrough routes) because those routes forward raw TLS bytes and the
// PROXY protocol header would corrupt the TLS handshake.
func withUpstreamProxyProtocol() ClusterMutator {
	return func(cluster *envoy_config_cluster_v3.Cluster) *envoy_config_cluster_v3.Cluster {
		if cluster == nil {
			return cluster
		}
		innerSocket := cluster.TransportSocket
		if innerSocket == nil {
			innerSocket = &envoy_config_core_v3.TransportSocket{
				Name: rawBufferTransportSocketName,
			}
		}
		cluster.TransportSocket = &envoy_config_core_v3.TransportSocket{
			Name: proxyProtocolTransportSocketName,
			ConfigType: &envoy_config_core_v3.TransportSocket_TypedConfig{
				TypedConfig: toAny(&envoy_transport_sockets_proxy_protocol_v3.ProxyProtocolUpstreamTransport{
					Config: &envoy_config_core_v3.ProxyProtocolConfig{
						Version: envoy_config_core_v3.ProxyProtocolConfig_V2,
					},
					TransportSocket: innerSocket,
				}),
			},
		}
		return cluster
	}
}
