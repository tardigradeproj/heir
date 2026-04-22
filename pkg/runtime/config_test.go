package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMergeArgs(t *testing.T) {
	tests := []struct {
		name     string
		defaults map[string]string
		extra    map[string]string
		expected map[string]string
	}{
		{
			name:     "nil extra returns defaults unchanged",
			defaults: map[string]string{"v": "2", "bind-address": "0.0.0.0"},
			extra:    nil,
			expected: map[string]string{"v": "2", "bind-address": "0.0.0.0"},
		},
		{
			name:     "nil defaults returns extra unchanged",
			defaults: nil,
			extra:    map[string]string{"v": "5"},
			expected: map[string]string{"v": "5"},
		},
		{
			name:     "extra overrides matching keys in defaults",
			defaults: map[string]string{"v": "2", "bind-address": "0.0.0.0"},
			extra:    map[string]string{"v": "5"},
			expected: map[string]string{"v": "5", "bind-address": "0.0.0.0"},
		},
		{
			name:     "extra keys not in defaults are added",
			defaults: map[string]string{"v": "2"},
			extra:    map[string]string{"feature-gates": "SomeFeature=true"},
			expected: map[string]string{"v": "2", "feature-gates": "SomeFeature=true"},
		},
		{
			name:     "both nil returns empty map",
			defaults: nil,
			extra:    nil,
			expected: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MergeArgs(tt.defaults, tt.extra)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestRenderRunScript(t *testing.T) {
	tests := []struct {
		name     string
		binary   string
		args     map[string]string
		expected string
	}{
		{
			name:   "no args produces exec-only script",
			binary: "/usr/local/bin/kube-apiserver",
			args:   map[string]string{},
			expected: "#!/bin/sh\n" +
				"exec /usr/local/bin/kube-apiserver",
		},
		{
			name:   "nil args produces exec-only script",
			binary: "/usr/local/bin/kine",
			args:   nil,
			expected: "#!/bin/sh\n" +
				"exec /usr/local/bin/kine",
		},
		{
			name:   "single arg is rendered without trailing backslash",
			binary: "/usr/local/bin/kube-scheduler",
			args:   map[string]string{"leader-elect": "true"},
			expected: "#!/bin/sh\n" +
				"exec /usr/local/bin/kube-scheduler \\\n" +
				"  --leader-elect=true",
		},
		{
			name:   "multiple args are sorted and last has no trailing backslash",
			binary: "/usr/local/bin/kube-controller-manager",
			args: map[string]string{
				"v":             "2",
				"bind-address":  "0.0.0.0",
				"cluster-cidr":  "10.244.0.0/16",
			},
			expected: "#!/bin/sh\n" +
				"exec /usr/local/bin/kube-controller-manager \\\n" +
				"  --bind-address=0.0.0.0 \\\n" +
				"  --cluster-cidr=10.244.0.0/16 \\\n" +
				"  --v=2",
		},
		{
			name:   "args with empty value are rendered",
			binary: "/usr/local/bin/kube-apiserver",
			args:   map[string]string{"feature-gates": ""},
			expected: "#!/bin/sh\n" +
				"exec /usr/local/bin/kube-apiserver \\\n" +
				"  --feature-gates=",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RenderRunScript(tt.binary, tt.args)
			assert.Equal(t, tt.expected, got)
		})
	}
}
