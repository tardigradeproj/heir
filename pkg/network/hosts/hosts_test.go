package hosts

import (
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

const systemLines = "127.0.0.1\tlocalhost\n::1\t\tlocalhost ip6-localhost\n"

func newTestManager(t *testing.T, initial string) *Manager {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "hosts")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(initial); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return New(f.Name())
}

func fileContent(t *testing.T, m *Manager) string {
	t.Helper()
	b, err := os.ReadFile(m.path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSync(t *testing.T) {
	tests := []struct {
		name      string
		initial   string
		entries   map[string]string
		assertion func(t *testing.T, got string)
	}{
		{
			name:    "adds entries",
			initial: systemLines,
			entries: map[string]string{
				"worker.az1": "127.0.1.1",
				"worker.az2": "127.0.1.2",
			},
			assertion: func(t *testing.T, got string) {
				assert.Contains(t, got, "127.0.1.1\tworker.az1\t# plane-tunnel")
				assert.Contains(t, got, "127.0.1.2\tworker.az2\t# plane-tunnel")
			},
		},
		{
			name:    "preserves system lines",
			initial: systemLines,
			entries: map[string]string{"worker.az1": "127.0.1.1"},
			assertion: func(t *testing.T, got string) {
				assert.Contains(t, got, "127.0.0.1\tlocalhost")
				assert.Contains(t, got, "::1\t\tlocalhost ip6-localhost")
			},
		},
		{
			name:    "updates existing entry to new IP",
			initial: systemLines + "127.0.1.1\tworker.az1\t# plane-tunnel\n",
			entries: map[string]string{"worker.az1": "127.0.1.5"},
			assertion: func(t *testing.T, got string) {
				assert.Contains(t, got, "127.0.1.5\tworker.az1\t# plane-tunnel")
				assert.NotContains(t, got, "127.0.1.1\tworker.az1")
			},
		},
		{
			name: "removes stale entries absent from the map",
			initial: systemLines +
				"127.0.1.1\tworker.az1\t# plane-tunnel\n" +
				"127.0.1.2\tworker.az2\t# plane-tunnel\n",
			entries: map[string]string{"worker.az1": "127.0.1.1"},
			assertion: func(t *testing.T, got string) {
				assert.Contains(t, got, "worker.az1")
				assert.NotContains(t, got, "worker.az2")
			},
		},
		{
			name: "empty map removes all managed entries",
			initial: systemLines +
				"127.0.1.1\tworker.az1\t# plane-tunnel\n" +
				"127.0.1.2\tworker.az2\t# plane-tunnel\n",
			entries: nil,
			assertion: func(t *testing.T, got string) {
				assert.NotContains(t, got, "plane-tunnel")
				assert.Contains(t, got, "127.0.0.1\tlocalhost")
			},
		},
		{
			name:    "repeated syncs produce no duplicate entries",
			initial: systemLines,
			entries: map[string]string{"worker.az1": "127.0.1.1"},
			assertion: func(t *testing.T, got string) {
				assert.Equal(t, 1, strings.Count(got, "worker.az1"))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestManager(t, tc.initial)

			err := m.Sync(tc.entries)
			assert.NoError(t, err)

			tc.assertion(t, fileContent(t, m))
		})
	}
}

func TestSync_Concurrent(t *testing.T) {
	m := newTestManager(t, systemLines)

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.Sync(map[string]string{"worker.az1": "127.0.1.1"})
		}()
	}
	wg.Wait()

	got := fileContent(t, m)
	assert.Contains(t, got, "127.0.0.1\tlocalhost")
	assert.Equal(t, 1, strings.Count(got, "worker.az1"))
}
