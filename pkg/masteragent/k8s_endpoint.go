package masteragent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/tardigrade-runtime/samaritano/pkg/k8s"
	workertyp "github.com/tardigrade-runtime/samaritano/pkg/provision/worker/typ"
	pkgruntime "github.com/tardigrade-runtime/samaritano/pkg/runtime"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	k8sSvcNamespace      = "default"
	k8sSvcName           = "kubernetes"
	endpointSyncInterval = 15 * time.Second
	defaultAPIServerPort = int32(6443)
)

// SyncKubernetesEndpoints reads the node profile configmap for external addresses,
// resolves their DNS, and keeps the kubernetes service EndpointSlice up to date.
// It syncs immediately on start and then every 15 seconds.
func SyncKubernetesEndpoints(ctx context.Context) error {
	layout := pkgruntime.NewControlPlaneLayout()

	client, err := k8s.BuildClient(layout.Auth.AdminConf.MountPath, "")
	if err != nil {
		return err
	}

	wctx := workertyp.NewWorkerContextWithDefaults()

	if err := doSyncEndpoints(ctx, client, wctx); err != nil {
		log.WithError(err).Error("initial kubernetes endpoint sync failed")
	}

	ticker := time.NewTicker(endpointSyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := doSyncEndpoints(ctx, client, wctx); err != nil {
				log.WithError(err).Error("kubernetes endpoint sync failed")
			}
		}
	}
}

type resolvedEndpoint struct {
	ip   string
	port int32
}

func parseExternalAddress(addr string) (host string, port int32, err error) {
	u, err := url.Parse(addr)
	if err != nil {
		return "", 0, fmt.Errorf("parse %q: %w", addr, err)
	}
	host = u.Hostname()
	if host == "" {
		return "", 0, fmt.Errorf("no hostname in %q", addr)
	}
	if portStr := u.Port(); portStr != "" {
		p, err := strconv.ParseInt(portStr, 10, 32)
		if err != nil {
			return "", 0, fmt.Errorf("parse port in %q: %w", addr, err)
		}
		return host, int32(p), nil
	}
	return host, defaultAPIServerPort, nil
}

func doSyncEndpoints(ctx context.Context, client kubernetes.Interface, wctx *workertyp.WorkerContext) error {
	//#TODO: use Informer
	cm, err := client.CoreV1().ConfigMaps("kube-system").Get(ctx, wctx.WorkerProfileConfigMapName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get configmap %q: %w", wctx.WorkerProfileConfigMapName, err)
	}

	raw, ok := cm.Data[wctx.ExternalAddressNodeProfileConfigmapKey]
	if !ok {
		return fmt.Errorf("configmap %q missing key %q", wctx.WorkerProfileConfigMapName, wctx.ExternalAddressNodeProfileConfigmapKey)
	}

	var rawAddresses []string
	if err := json.Unmarshal([]byte(raw), &rawAddresses); err != nil {
		return fmt.Errorf("unmarshal external addresses: %w", err)
	}

	var resolved []resolvedEndpoint
	for _, addr := range rawAddresses {
		host, port, err := parseExternalAddress(addr)
		if err != nil {
			log.WithError(err).WithField("address", addr).Warn("failed to parse external address, skipping")
			continue
		}
		ips, err := net.LookupHost(host)
		if err != nil {
			log.WithError(err).WithField("host", host).Warn("dns resolution failed, skipping")
			continue
		}
		for _, ip := range ips {
			resolved = append(resolved, resolvedEndpoint{ip: ip, port: port})
		}
	}

	if len(resolved) == 0 {
		return fmt.Errorf("no IPs resolved from external addresses %v", rawAddresses)
	}

	ready := true
	endpoints := make([]discoveryv1.Endpoint, 0, len(resolved))
	for _, r := range resolved {
		endpoints = append(endpoints, discoveryv1.Endpoint{
			Addresses:  []string{r.ip},
			Conditions: discoveryv1.EndpointConditions{Ready: &ready},
		})
	}

	// Collect unique ports across all resolved addresses.
	portSeen := map[int32]struct{}{}
	proto := corev1.ProtocolTCP
	portName := "https"
	var ports []discoveryv1.EndpointPort
	for _, r := range resolved {
		if _, ok := portSeen[r.port]; ok {
			continue
		}
		portSeen[r.port] = struct{}{}
		p := r.port
		ports = append(ports, discoveryv1.EndpointPort{Name: &portName, Protocol: &proto, Port: &p})
	}

	slices, err := client.DiscoveryV1().EndpointSlices(k8sSvcNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "kubernetes.io/service-name=" + k8sSvcName,
	})
	if err != nil {
		return fmt.Errorf("list endpoint slices: %w", err)
	}

	resolvedIPs := make([]string, 0, len(resolved))
	for _, r := range resolved {
		resolvedIPs = append(resolvedIPs, r.ip)
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
	log.WithField("ips", resolvedIPs).Info("updated kubernetes endpoint slice")
	return nil
}
