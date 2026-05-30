// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package ingestion

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/stretchr/testify/require"
)

func Test_backendRefToModelBackend_proxyProtocolLabel(t *testing.T) {
	port := gatewayv1.PortNumber(8443)
	be := gatewayv1.BackendObjectReference{
		Name: "my-svc",
		Port: &port,
	}
	makeSvc := func(labelVal string) corev1.Service {
		s := corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "my-svc", Namespace: "default"},
			Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 8443}}},
		}
		if labelVal != "" {
			s.Labels = map[string]string{"service.cilium.io/proxy-protocol": labelVal}
		}
		return s
	}

	t.Run("label v2 → UpstreamProxyProtocol=v2", func(t *testing.T) {
		result := backendRefToModelBackend(makeSvc("v2"), be, "default")
		require.Equal(t, "v2", result.UpstreamProxyProtocol)
	})

	t.Run("label v1 → UpstreamProxyProtocol=v1", func(t *testing.T) {
		result := backendRefToModelBackend(makeSvc("v1"), be, "default")
		require.Equal(t, "v1", result.UpstreamProxyProtocol)
	})

	t.Run("no label → UpstreamProxyProtocol empty", func(t *testing.T) {
		result := backendRefToModelBackend(makeSvc(""), be, "default")
		require.Equal(t, "", result.UpstreamProxyProtocol)
	})

	t.Run("label 'true' → UpstreamProxyProtocol empty (invalid)", func(t *testing.T) {
		result := backendRefToModelBackend(makeSvc("true"), be, "default")
		require.Equal(t, "", result.UpstreamProxyProtocol, "'true' is not a valid version, must be disabled")
	})

	t.Run("label 'V2' (uppercase) → UpstreamProxyProtocol empty (invalid)", func(t *testing.T) {
		result := backendRefToModelBackend(makeSvc("V2"), be, "default")
		require.Equal(t, "", result.UpstreamProxyProtocol, "'V2' uppercase is not valid")
	})
}
