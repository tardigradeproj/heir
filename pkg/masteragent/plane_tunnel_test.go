package masteragent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tardigradeproj/heir/pkg/network/hosts"
)

func TestFetchReport(t *testing.T) {
	tests := []struct {
		name      string
		handler   http.HandlerFunc
		assertion func(t *testing.T, report *ReportResponse, err error)
	}{
		{
			name: "returns connected nodes on 200",
			handler: func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(ReportResponse{
					Node: []ConnectedNode{{Name: "worker2"}, {Name: "worker5"}},
				})
			},
			assertion: func(t *testing.T, report *ReportResponse, err error) {
				require.NoError(t, err)
				require.NotNil(t, report)
				names := make([]string, 0, len(report.Node))
				for _, n := range report.Node {
					names = append(names, n.Name)
				}
				assert.ElementsMatch(t, []string{"worker2", "worker5"}, names)
			},
		},
		{
			name: "returns empty node list on empty report",
			handler: func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(ReportResponse{})
			},
			assertion: func(t *testing.T, report *ReportResponse, err error) {
				require.NoError(t, err)
				require.NotNil(t, report)
				assert.Empty(t, report.Node)
			},
		},
		{
			name: "error on non-200 status",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			assertion: func(t *testing.T, report *ReportResponse, err error) {
				assert.Error(t, err)
				assert.Nil(t, report)
			},
		},
		{
			name: "error on malformed JSON body",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("{invalid"))
			},
			assertion: func(t *testing.T, report *ReportResponse, err error) {
				assert.Error(t, err)
				assert.Nil(t, report)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()

			report, err := fetchReport(context.Background(), srv.Listener.Addr().String())

			tt.assertion(t, report, err)
		})
	}
}

func TestReconcile(t *testing.T) {
	tests := []struct {
		name        string
		planeTunnel func(hostsPath string) *PlaneTunnel
		assertion   func(t *testing.T, err error, hostsPath string)
	}{
		{
			name: "single pod single worker",
			planeTunnel: func(hostsPath string) *PlaneTunnel {
				return &PlaneTunnel{
					hostsManager: hosts.New(hostsPath),
					resolvePods: func(_ context.Context, _ string) ([]string, error) {
						return []string{"10.0.0.1"}, nil
					},
					fetchReport: func(_ context.Context, _ string) (*ReportResponse, error) {
						return &ReportResponse{Node: []ConnectedNode{{Name: "worker1"}}}, nil
					},
				}
			},
			assertion: func(t *testing.T, err error, hostsPath string) {
				require.NoError(t, err)
				assert.Equal(t, map[string]string{
					"worker1": "10.0.0.1",
				}, readHostsEntries(t, hostsPath))
			},
		},
		{
			name: "multiple workers on same pod share pod IP",
			planeTunnel: func(hostsPath string) *PlaneTunnel {
				return &PlaneTunnel{
					hostsManager: hosts.New(hostsPath),
					resolvePods: func(_ context.Context, _ string) ([]string, error) {
						return []string{"10.0.0.1"}, nil
					},
					fetchReport: func(_ context.Context, _ string) (*ReportResponse, error) {
						return &ReportResponse{Node: []ConnectedNode{{Name: "worker1"}, {Name: "worker2"}}}, nil
					},
				}
			},
			assertion: func(t *testing.T, err error, hostsPath string) {
				require.NoError(t, err)
				assert.Equal(t, map[string]string{
					"worker1": "10.0.0.1",
					"worker2": "10.0.0.1",
				}, readHostsEntries(t, hostsPath))
			},
		},
		{
			name: "workers distributed across pods",
			planeTunnel: func(hostsPath string) *PlaneTunnel {
				reports := map[string][]ConnectedNode{
					"10.0.0.1": {{Name: "worker1"}, {Name: "worker2"}},
					"10.0.0.2": {{Name: "worker3"}},
				}
				return &PlaneTunnel{
					hostsManager: hosts.New(hostsPath),
					resolvePods: func(_ context.Context, _ string) ([]string, error) {
						return []string{"10.0.0.1", "10.0.0.2"}, nil
					},
					fetchReport: func(_ context.Context, podIP string) (*ReportResponse, error) {
						return &ReportResponse{Node: reports[podIP]}, nil
					},
				}
			},
			assertion: func(t *testing.T, err error, hostsPath string) {
				require.NoError(t, err)
				assert.Equal(t, map[string]string{
					"worker1": "10.0.0.1",
					"worker2": "10.0.0.1",
					"worker3": "10.0.0.2",
				}, readHostsEntries(t, hostsPath))
			},
		},
		{
			name: "resolver error stops reconcile without touching hosts",
			planeTunnel: func(hostsPath string) *PlaneTunnel {
				return &PlaneTunnel{
					hostsManager: hosts.New(hostsPath),
					resolvePods: func(_ context.Context, _ string) ([]string, error) {
						return nil, fmt.Errorf("dns failure")
					},
					fetchReport: func(_ context.Context, _ string) (*ReportResponse, error) {
						return &ReportResponse{}, nil
					},
				}
			},
			assertion: func(t *testing.T, err error, hostsPath string) {
				assert.Error(t, err)
				assert.Empty(t, readHostsEntries(t, hostsPath))
			},
		},
		{
			name: "failed pod report is skipped, others proceed",
			planeTunnel: func(hostsPath string) *PlaneTunnel {
				return &PlaneTunnel{
					hostsManager: hosts.New(hostsPath),
					resolvePods: func(_ context.Context, _ string) ([]string, error) {
						return []string{"10.0.0.1", "10.0.0.2"}, nil
					},
					fetchReport: func(_ context.Context, podIP string) (*ReportResponse, error) {
						if podIP == "10.0.0.1" {
							return nil, fmt.Errorf("connection refused")
						}
						return &ReportResponse{Node: []ConnectedNode{{Name: "worker3"}}}, nil
					},
				}
			},
			assertion: func(t *testing.T, err error, hostsPath string) {
				require.NoError(t, err)
				assert.Equal(t, map[string]string{
					"worker3": "10.0.0.2",
				}, readHostsEntries(t, hostsPath))
			},
		},
		{
			name: "all pod reports fail syncs empty hosts",
			planeTunnel: func(hostsPath string) *PlaneTunnel {
				return &PlaneTunnel{
					hostsManager: hosts.New(hostsPath),
					resolvePods: func(_ context.Context, _ string) ([]string, error) {
						return []string{"10.0.0.1"}, nil
					},
					fetchReport: func(_ context.Context, _ string) (*ReportResponse, error) {
						return nil, fmt.Errorf("connection refused")
					},
				}
			},
			assertion: func(t *testing.T, err error, hostsPath string) {
				require.NoError(t, err)
				assert.Empty(t, readHostsEntries(t, hostsPath))
			},
		},
		{
			name: "no pods resolves to empty hosts",
			planeTunnel: func(hostsPath string) *PlaneTunnel {
				return &PlaneTunnel{
					hostsManager: hosts.New(hostsPath),
					resolvePods: func(_ context.Context, _ string) ([]string, error) {
						return []string{}, nil
					},
					fetchReport: func(_ context.Context, _ string) (*ReportResponse, error) {
						return &ReportResponse{}, nil
					},
				}
			},
			assertion: func(t *testing.T, err error, hostsPath string) {
				require.NoError(t, err)
				assert.Empty(t, readHostsEntries(t, hostsPath))
			},
		},
		{
			name: "empty report from pod syncs empty hosts",
			planeTunnel: func(hostsPath string) *PlaneTunnel {
				return &PlaneTunnel{
					hostsManager: hosts.New(hostsPath),
					resolvePods: func(_ context.Context, _ string) ([]string, error) {
						return []string{"10.0.0.1"}, nil
					},
					fetchReport: func(_ context.Context, _ string) (*ReportResponse, error) {
						return &ReportResponse{Node: []ConnectedNode{}}, nil
					},
				}
			},
			assertion: func(t *testing.T, err error, hostsPath string) {
				require.NoError(t, err)
				assert.Empty(t, readHostsEntries(t, hostsPath))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hostsPath := filepath.Join(t.TempDir(), "hosts")
			require.NoError(t, os.WriteFile(hostsPath, []byte(""), 0644))

			err := tt.planeTunnel(hostsPath).reconcile()

			tt.assertion(t, err, hostsPath)
		})
	}
}

// readHostsEntries parses plane-tunnel-managed lines from a hosts file and
// returns a hostname → IP map.
func readHostsEntries(t *testing.T, path string) map[string]string {
	t.Helper()

	content, err := os.ReadFile(path)
	require.NoError(t, err)

	entries := make(map[string]string)
	for _, line := range strings.Split(string(content), "\n") {
		if !strings.Contains(line, "# plane-tunnel") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			entries[fields[1]] = fields[0] // hostname → IP
		}
	}
	return entries
}
