package clusteragent

import (
	"context"
	"fmt"
	"net"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/tardigradeproj/heir/api/controlplane/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	k8sSvcNamespace      = "default"
	k8sSvcName           = "kubernetes"
	endpointSyncInterval = 15 * time.Second
)

// SyncKubernetesEndpoints resolves the tenant Runtime's external API server
// address via DNS (IPs are used directly) and keeps the kubernetes service
// EndpointSlice up to date. It syncs immediately on start and then every 15
// seconds.
func SyncKubernetesEndpoints(ctx context.Context, client kubernetes.Interface, runtimeObj *v1alpha1.Runtime) error {
	if err := doSyncEndpoints(ctx, client, runtimeObj); err != nil {
		log.WithError(err).Error("initial kubernetes endpoint sync failed")
	}

	ticker := time.NewTicker(endpointSyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := doSyncEndpoints(ctx, client, runtimeObj); err != nil {
				log.WithError(err).Error("kubernetes endpoint sync failed")
			}
		}
	}
}

type resolvedEndpoint struct {
	ip   string
	port int32
}

func doSyncEndpoints(ctx context.Context, client kubernetes.Interface, runtimeObj *v1alpha1.Runtime) error {
	cpe := runtimeObj.Spec.Cluster.ControlPlaneExternalEndpoint

	host := cpe.APIServer.Host
	port := cpe.APIServer.Port

	resolved, err := resolveIPv4Endpoints(host, port)
	if err != nil {
		return err
	}
	if len(resolved) == 0 {
		return fmt.Errorf("no IPv4 endpoints resolved from host %q", host)
	}

	ready := true
	endpoints := make([]discoveryv1.Endpoint, 0, len(resolved))
	for _, r := range resolved {
		endpoints = append(endpoints, discoveryv1.Endpoint{
			Addresses:  []string{r.ip},
			Conditions: discoveryv1.EndpointConditions{Ready: &ready},
		})
	}
	ports := []discoveryv1.EndpointPort{{Name: new("https"), Protocol: new(corev1.ProtocolTCP), Port: &port}}
	resolvedIPs := make([]string, 0, len(resolved))
	for _, r := range resolved {
		resolvedIPs = append(resolvedIPs, r.ip)
	}

	return applyEndpointSlice(ctx, client, endpoints, ports, resolvedIPs)
}

func resolveIPv4Endpoints(host string, port int32) ([]resolvedEndpoint, error) {
	log.WithField("address", host).Debug("resolving address")

	var candidates []string
	if ip := net.ParseIP(host); ip != nil {
		candidates = []string{host}
	} else if host != "" {
		ips, err := net.LookupHost(host)
		if err != nil {
			return nil, fmt.Errorf("resolve host %q: %w", host, err)
		}
		candidates = ips
	}

	resolved := make([]resolvedEndpoint, 0, len(candidates))
	for _, ip := range candidates {
		parsed := net.ParseIP(ip)
		if parsed == nil || parsed.To4() == nil {
			log.WithField("ip", ip).Warn("skipping non-IPv4 address")
			continue
		}
		resolved = append(resolved, resolvedEndpoint{ip: ip, port: port})
	}
	return resolved, nil
}

// applyEndpointSlice creates or updates the "kubernetes" service EndpointSlice with the
// given endpoints and ports.
func applyEndpointSlice(ctx context.Context, client kubernetes.Interface, endpoints []discoveryv1.Endpoint, ports []discoveryv1.EndpointPort, resolvedIPs []string) error {
	slices, err := client.DiscoveryV1().EndpointSlices(k8sSvcNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "kubernetes.io/service-name=" + k8sSvcName,
	})
	if err != nil {
		return fmt.Errorf("list endpoint slices: %w", err)
	}

	if len(slices.Items) == 0 {
		_, err = client.DiscoveryV1().EndpointSlices(k8sSvcNamespace).Create(ctx, &discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:      k8sSvcName,
				Namespace: k8sSvcNamespace,
				Labels:    map[string]string{"kubernetes.io/service-name": k8sSvcName},
			},
			AddressType: discoveryv1.AddressTypeIPv4,
			Endpoints:   endpoints,
			Ports:       ports,
		}, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("create endpoint slice: %w", err)
		}
		log.WithField("ips", resolvedIPs).Info("created kubernetes endpoint slice")
		return nil
	}

	existing := slices.Items[0].DeepCopy()
	existing.Endpoints = endpoints
	existing.Ports = ports
	_, err = client.DiscoveryV1().EndpointSlices(k8sSvcNamespace).Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("update endpoint slice: %w", err)
	}
	log.WithField("ips", resolvedIPs).
		Info("updated kubernetes endpoint slice")
	return nil
}
