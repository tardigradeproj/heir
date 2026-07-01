package hosts

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
)

const marker = "# plane-tunnel"

// Manager manages plane-tunnel-owned entries in an /etc/hosts file without
// touching any lines it did not write.
type Manager struct {
	path string
	mu   sync.Mutex
}

func New(path string) *Manager {
	return &Manager{path: path}
}

// Sync replaces all managed entries with the provided hostname → IP map in a
// single write. Entries present in the file but absent from the map are
// removed. Pass an empty map to remove all managed entries.
func (m *Manager) Sync(entries map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	lines, err := m.read()
	if err != nil {
		return err
	}

	// Retain every line that is not managed by plane-tunnel.
	out := make([]string, 0, len(lines)+len(entries))
	for _, line := range lines {
		if !strings.Contains(line, marker) {
			out = append(out, line)
		}
	}

	for hostname, ip := range entries {
		out = append(out, fmt.Sprintf("%s\t%s\t%s", ip, hostname, marker))
	}

	return m.write(out)
}

func (m *Manager) read() ([]string, error) {
	f, err := os.Open(m.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		lines = append(lines, s.Text())
	}
	return lines, s.Err()
}

// write persists lines to the file atomically via a temp-file rename.
func (m *Manager) write(lines []string) error {
	content := strings.Join(lines, "\n") + "\n"
	tmp := m.path + ".plane-tunnel.tmp"
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}
