//go:build darwin

package procmgr

import (
	"context"
	"os"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// Component represents a long-running process.
type Component struct {
	LogLevel    log.Level
	LogFilePath string
	Name        string
	BinPath     string
	Args        []string
	// Env holds explicit key=value pairs always added to the process environment.
	Env []string
	// EnvInherit, when true, seeds the process environment from the parent
	// process keeping only variables whose names start with EnvPrefix.
	EnvInherit bool
	EnvPrefix  string
	// MaxRetries is the number of restart attempts after a crash.
	// Zero means run once with no retries.
	MaxRetries int
	// InitialBackoff is the wait time before the first restart attempt.
	InitialBackoff time.Duration
	// MaxBackoff is the ceiling for exponential backoff between restarts.
	MaxBackoff time.Duration
	// StopTimeout is how long Teardown waits for the process to exit after
	// SIGTERM before sending SIGKILL. Defaults to 10 seconds.
	StopTimeout time.Duration

	log      log.FieldLogger
	mu       sync.Mutex
	proc     *os.Process
	stopping bool
}

// Run starts the component and restarts it on failure up to MaxRetries times.
// It blocks until the retry budget is exhausted or ctx is cancelled.
func (c *Component) Run(ctx context.Context) error {
	return nil
}

func (c *Component) runOnce(ctx context.Context) error {
	return nil
}

// Teardown sends SIGTERM to the running process and waits for it to exit.
// If the process does not exit within StopTimeout, SIGKILL is sent.
func (c *Component) Teardown(ctx context.Context) error {
	return nil
}
