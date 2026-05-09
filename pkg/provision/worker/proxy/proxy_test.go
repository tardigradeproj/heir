package proxy

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_parseApiServerExternalAddresses(t *testing.T) {
	type args struct {
		rawURLs []string
	}
	tests := []struct {
		name      string
		args      args
		assertion func(t *testing.T, want []string, err error)
	}{
		{
			name: "empty input",
			args: args{
				rawURLs: []string{},
			},
			assertion: func(t *testing.T, want []string, err error) {
				assert.NoError(t, err)
				assert.Empty(t, want)
			},
		},
		{
			name: "url with protocol, IP and port",
			args: args{
				rawURLs: []string{"https://127.0.0.1:7474"},
			},
			assertion: func(t *testing.T, want []string, err error) {
				assert.NoError(t, err)
				assert.ElementsMatch(t, want, []string{"127.0.0.1:7474"})
			},
		},
		{
			name: "duplicated urls",
			args: args{
				rawURLs: []string{"https://127.0.0.1:7474", "https://127.0.0.1:7474"},
			},
			assertion: func(t *testing.T, want []string, err error) {
				assert.NoError(t, err)
				assert.ElementsMatch(t, want, []string{"127.0.0.1:7474"})
			},
		},
		{
			name: "url and IP with no port",
			args: args{
				rawURLs: []string{"https://api.com:7474", "https://127.0.0.1"},
			},
			assertion: func(t *testing.T, want []string, err error) {
				assert.NoError(t, err)
				fmt.Println(want)
				assert.ElementsMatch(t, want, []string{"api.com:7474", "127.0.0.1"})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseApiServerExternalAddresses(tt.args.rawURLs)
			tt.assertion(t, got, err)
		})
	}
}
