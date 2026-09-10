/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package clusteragent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tardigradeproj/heir/api/controlplane/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// newRuntimeObj builds a Runtime whose ControlPlaneExternalEndpoint.APIServer is host:port.
func newRuntimeObj(host string, port int32) *v1alpha1.Runtime {
	return &v1alpha1.Runtime{
		Spec: v1alpha1.RuntimeSpec{
			Cluster: v1alpha1.ClusterSpec{
				ControlPlaneExternalEndpoint: v1alpha1.ControlPlaneExternalEndpointSpec{
					APIServer: v1alpha1.ComponentEndpoint{Host: host, Port: port},
				},
			},
		},
	}
}

// newExistingSlice builds a "kubernetes" EndpointSlice as it would already exist in-cluster.
func newExistingSlice(endpoints []discoveryv1.Endpoint, ports []discoveryv1.EndpointPort) *discoveryv1.EndpointSlice {
	return &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k8sSvcName,
			Namespace: k8sSvcNamespace,
			Labels:    map[string]string{"kubernetes.io/service-name": k8sSvcName},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints:   endpoints,
		Ports:       ports,
	}
}

func getSlice(t *testing.T, fc *fake.Clientset) *discoveryv1.EndpointSlice {
	t.Helper()
	slice, err := fc.DiscoveryV1().EndpointSlices(k8sSvcNamespace).Get(context.Background(), k8sSvcName, metav1.GetOptions{})
	require.NoError(t, err)
	return slice
}

// ---- resolveIPv4Endpoints ----

func TestResolveIPv4Endpoints(t *testing.T) {
	tests := []struct {
		name string
		host string
		port int32
		want []resolvedEndpoint
	}{
		{
			name: "IPv4 literal is used directly without DNS",
			host: "203.0.113.5",
			port: 6443,
			want: []resolvedEndpoint{{ip: "203.0.113.5", port: 6443}},
		},
		{
			name: "IPv6 literal is filtered out as non-IPv4",
			host: "2001:db8::1",
			port: 6443,
			want: []resolvedEndpoint{},
		},
		{
			name: "empty host resolves to nothing",
			host: "",
			port: 6443,
			want: []resolvedEndpoint{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveIPv4Endpoints(tt.host, tt.port)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ---- applyEndpointSlice ----

func TestApplyEndpointSlice(t *testing.T) {
	ready := true
	newEndpoints := []discoveryv1.Endpoint{{
		Addresses:  []string{"10.0.0.9"},
		Conditions: discoveryv1.EndpointConditions{Ready: &ready},
	}}
	newPorts := []discoveryv1.EndpointPort{{Name: new("https"), Protocol: new(corev1.ProtocolTCP), Port: new(int32(6443))}}

	staleEndpoints := []discoveryv1.Endpoint{{
		Addresses:  []string{"10.0.0.1"},
		Conditions: discoveryv1.EndpointConditions{Ready: &ready},
	}}
	stalePorts := []discoveryv1.EndpointPort{{Name: new("https"), Protocol: new(corev1.ProtocolTCP), Port: new(int32(30080))}}

	tests := []struct {
		name        string
		existing    []runtime.Object
		reactor     func(fc *fake.Clientset)
		wantErr     bool
		errContains string
		validate    func(t *testing.T, fc *fake.Clientset)
	}{
		{
			name: "creates the slice when none exists",
			validate: func(t *testing.T, fc *fake.Clientset) {
				slice := getSlice(t, fc)
				assert.Equal(t, discoveryv1.AddressTypeIPv4, slice.AddressType)
				assert.Equal(t, newEndpoints, slice.Endpoints)
				assert.Equal(t, newPorts, slice.Ports)
				assert.Equal(t, map[string]string{"kubernetes.io/service-name": k8sSvcName}, slice.Labels)
			},
		},
		{
			name:     "updates the existing slice's endpoints and ports",
			existing: []runtime.Object{newExistingSlice(staleEndpoints, stalePorts)},
			validate: func(t *testing.T, fc *fake.Clientset) {
				slice := getSlice(t, fc)
				assert.Equal(t, newEndpoints, slice.Endpoints)
				assert.Equal(t, newPorts, slice.Ports)
			},
		},
		{
			name: "list error is surfaced",
			reactor: func(fc *fake.Clientset) {
				fc.PrependReactor("list", "endpointslices", func(action k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, assert.AnError
				})
			},
			wantErr:     true,
			errContains: "list endpoint slices",
		},
		{
			name: "create error is surfaced",
			reactor: func(fc *fake.Clientset) {
				fc.PrependReactor("create", "endpointslices", func(action k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, assert.AnError
				})
			},
			wantErr:     true,
			errContains: "create endpoint slice",
		},
		{
			name:     "update error is surfaced",
			existing: []runtime.Object{newExistingSlice(staleEndpoints, stalePorts)},
			reactor: func(fc *fake.Clientset) {
				fc.PrependReactor("update", "endpointslices", func(action k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, assert.AnError
				})
			},
			wantErr:     true,
			errContains: "update endpoint slice",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fc := fake.NewClientset(tt.existing...)
			if tt.reactor != nil {
				tt.reactor(fc)
			}

			err := applyEndpointSlice(context.Background(), fc, newEndpoints, newPorts, []string{"10.0.0.9"})

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				return
			}
			require.NoError(t, err)
			tt.validate(t, fc)
		})
	}
}

// ---- doSyncEndpoints ----

func TestDoSyncEndpoints(t *testing.T) {
	tests := []struct {
		name        string
		host        string
		port        int32
		reactor     func(fc *fake.Clientset)
		wantErr     bool
		errContains string
		validate    func(t *testing.T, fc *fake.Clientset)
	}{
		{
			name: "IPv4 literal host creates a matching endpoint slice",
			host: "10.0.0.9",
			port: 6443,
			validate: func(t *testing.T, fc *fake.Clientset) {
				slice := getSlice(t, fc)
				require.Len(t, slice.Endpoints, 1)
				assert.Equal(t, []string{"10.0.0.9"}, slice.Endpoints[0].Addresses)
				require.Len(t, slice.Ports, 1)
				assert.Equal(t, int32(6443), *slice.Ports[0].Port)
				assert.Equal(t, "https", *slice.Ports[0].Name)
				assert.Equal(t, corev1.ProtocolTCP, *slice.Ports[0].Protocol)
			},
		},
		{
			name:        "no IPv4 endpoints resolved returns error",
			host:        "",
			port:        6443,
			wantErr:     true,
			errContains: "no IPv4 endpoints resolved",
		},
		{
			name: "endpoint slice apply failure is propagated",
			host: "10.0.0.9",
			port: 6443,
			reactor: func(fc *fake.Clientset) {
				fc.PrependReactor("list", "endpointslices", func(action k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, assert.AnError
				})
			},
			wantErr:     true,
			errContains: "list endpoint slices",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fc := fake.NewClientset()
			if tt.reactor != nil {
				tt.reactor(fc)
			}

			err := doSyncEndpoints(context.Background(), fc, newRuntimeObj(tt.host, tt.port))

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				return
			}
			require.NoError(t, err)
			tt.validate(t, fc)
		})
	}
}
