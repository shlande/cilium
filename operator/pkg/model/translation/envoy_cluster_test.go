// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package translation

import (
	"testing"

	envoy_config_cluster_v3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	envoy_config_core_v3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	envoy_transport_sockets_proxy_protocol_v3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/proxy_protocol/v3"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func Test_withClusterLbPolicy(t *testing.T) {
	fn := withClusterLbPolicy(int32(envoy_config_cluster_v3.Cluster_LEAST_REQUEST))

	t.Run("input is nil", func(t *testing.T) {
		cluster := fn(nil)
		require.Nil(t, cluster)
	})

	t.Run("input is not nil", func(t *testing.T) {
		cluster := &envoy_config_cluster_v3.Cluster{}
		cluster = fn(cluster)
		require.Equal(t, envoy_config_cluster_v3.Cluster_LEAST_REQUEST, cluster.LbPolicy)
	})
}

func Test_withOutlierDetection(t *testing.T) {
	t.Run("input is nil", func(t *testing.T) {
		fn := withOutlierDetection(true)
		cluster := fn(nil)
		require.Nil(t, cluster)
	})

	t.Run("input is not nil", func(t *testing.T) {
		t.Run("enabled", func(t *testing.T) {
			fn := withOutlierDetection(true)
			cluster := &envoy_config_cluster_v3.Cluster{}
			cluster = fn(cluster)
			require.NotNil(t, cluster.OutlierDetection)
			require.True(t, cluster.OutlierDetection.SplitExternalLocalOriginErrors)
		})

		t.Run("disabled", func(t *testing.T) {
			fn := withOutlierDetection(false)
			cluster := &envoy_config_cluster_v3.Cluster{}
			cluster = fn(cluster)
			require.NotNil(t, cluster.OutlierDetection)
			require.False(t, cluster.OutlierDetection.SplitExternalLocalOriginErrors)
		})
	})
}

func Test_withConnectionTimeout(t *testing.T) {
	fn := withConnectionTimeout(10)

	t.Run("input is nil", func(t *testing.T) {
		cluster := fn(nil)
		require.Nil(t, cluster)
	})

	t.Run("input is not nil", func(t *testing.T) {
		cluster := &envoy_config_cluster_v3.Cluster{}
		cluster = fn(cluster)
		require.Equal(t, int64(10), cluster.ConnectTimeout.Seconds)
	})
}

func Test_httpCluster(t *testing.T) {
	c := &cecTranslator{}
	res, err := c.httpCluster("dummy-name", "dummy-name", false, "")
	require.NoError(t, err)

	cluster := &envoy_config_cluster_v3.Cluster{}
	err = proto.Unmarshal(res.Value, cluster)

	require.NoError(t, err)
	require.Equal(t, "dummy-name", cluster.Name)
	require.Equal(t, &envoy_config_cluster_v3.Cluster_Type{
		Type: envoy_config_cluster_v3.Cluster_EDS,
	}, cluster.ClusterDiscoveryType)
}

func Test_tcpCluster(t *testing.T) {
	c := &cecTranslator{}
	res, err := c.httpCluster("dummy-name", "dummy-name", false, "")
	require.NoError(t, err)

	cluster := &envoy_config_cluster_v3.Cluster{}
	err = proto.Unmarshal(res.Value, cluster)

	require.NoError(t, err)
	require.Equal(t, "dummy-name", cluster.Name)
	require.Equal(t, &envoy_config_cluster_v3.Cluster_Type{
		Type: envoy_config_cluster_v3.Cluster_EDS,
	}, cluster.ClusterDiscoveryType)
}

func Test_withUpstreamProxyProtocol(t *testing.T) {
	fn := withUpstreamProxyProtocol(envoy_config_core_v3.ProxyProtocolConfig_V2)

	t.Run("input is nil", func(t *testing.T) {
		cluster := fn(nil)
		require.Nil(t, cluster)
	})

	t.Run("sets proxy protocol transport socket", func(t *testing.T) {
		cluster := &envoy_config_cluster_v3.Cluster{}
		cluster = fn(cluster)
		require.NotNil(t, cluster.TransportSocket)
		require.Equal(t, proxyProtocolTransportSocketName, cluster.TransportSocket.Name)
	})

	t.Run("nil transport socket uses raw_buffer fallback", func(t *testing.T) {
		cluster := &envoy_config_cluster_v3.Cluster{}
		// TransportSocket is nil by default
		cluster = fn(cluster)
		// Unmarshal the TypedConfig to verify the inner socket
		ppTransport := &envoy_transport_sockets_proxy_protocol_v3.ProxyProtocolUpstreamTransport{}
		err := cluster.TransportSocket.GetTypedConfig().UnmarshalTo(ppTransport)
		require.NoError(t, err)
		require.NotNil(t, ppTransport.TransportSocket)
		require.Equal(t, rawBufferTransportSocketName, ppTransport.TransportSocket.Name)
	})

	t.Run("wraps existing transport socket", func(t *testing.T) {
		existingSocket := &envoy_config_core_v3.TransportSocket{
			Name: "envoy.transport_sockets.tls",
		}
		cluster := &envoy_config_cluster_v3.Cluster{
			TransportSocket: existingSocket,
		}
		cluster = fn(cluster)
		ppTransport := &envoy_transport_sockets_proxy_protocol_v3.ProxyProtocolUpstreamTransport{}
		err := cluster.TransportSocket.GetTypedConfig().UnmarshalTo(ppTransport)
		require.NoError(t, err)
		require.NotNil(t, ppTransport.TransportSocket)
		require.Equal(t, "envoy.transport_sockets.tls", ppTransport.TransportSocket.Name)
	})

	t.Run("sets PROXY protocol V2", func(t *testing.T) {
		cluster := &envoy_config_cluster_v3.Cluster{}
		cluster = fn(cluster)
		ppTransport := &envoy_transport_sockets_proxy_protocol_v3.ProxyProtocolUpstreamTransport{}
		err := cluster.TransportSocket.GetTypedConfig().UnmarshalTo(ppTransport)
		require.NoError(t, err)
		require.NotNil(t, ppTransport.Config)
		require.Equal(t, envoy_config_core_v3.ProxyProtocolConfig_V2, ppTransport.Config.Version)
	})
}


