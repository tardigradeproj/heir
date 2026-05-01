//go:build linux

package procmgr

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/DeRuina/timberjack"
	retry "github.com/avast/retry-go"
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
	c.log = log.WithField("component", c.Name)
	return retry.Do(
		func() error { return c.runOnce(ctx) },
		retry.Attempts(uint(c.MaxRetries)+1),
		retry.Delay(c.InitialBackoff),
		retry.MaxDelay(c.MaxBackoff),
		retry.DelayType(retry.BackOffDelay),
		retry.Context(ctx),
		retry.OnRetry(func(n uint, err error) {
			log.WithField("component", c.Name).
				WithField("attempt", n+1).
				WithError(err).
				Warn("component exited unexpectedly, restarting")
		}),
	)
}

func (c *Component) runOnce(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, c.BinPath, c.Args...)
	cmd.Env = c.buildEnv()
	stdOut := io.Writer(os.Stdout)
	stdErr := io.Writer(os.Stderr)
	if c.LogFilePath != "" {
		fileLogOut := &timberjack.Logger{
			Filename:           c.LogFilePath,
			MaxSize:            80,
			MaxBackups:         3,
			MaxAge:             28,
			Compression:        "gzip",
			LocalTime:          true,
			RotationInterval:   24 * time.Hour,
			RotateAtMinutes:    []int{0, 15, 30, 45},
			RotateAt:           []string{"00:00", "12:00"},
			BackupTimeFormat:   "2006-01-02-15-04-05",
			AppendTimeAfterExt: true,
			FileMode:           0o644,
		}
		if c.LogLevel == log.DebugLevel {
			stdOut = io.MultiWriter(os.Stdout, fileLogOut)
			stdErr = io.MultiWriter(os.Stderr, fileLogOut)
		} else {
			stdOut = fileLogOut
			stdErr = fileLogOut
		}
	}
	cmd.Stdout = stdOut
	cmd.Stderr = stdErr
	// Ensure the child process is killed when the parent dies.
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", c.Name, err)
	}

	c.mu.Lock()
	c.proc = cmd.Process
	c.mu.Unlock()

	c.log.WithField("pid", cmd.Process.Pid).Info("process started")

	err := cmd.Wait()

	c.mu.Lock()
	stopping := c.stopping
	c.mu.Unlock()

	// Intentional stop — do not retry.
	if stopping {
		return retry.Unrecoverable(fmt.Errorf("%s stopped", c.Name))
	}
	if err != nil {
		return fmt.Errorf("%s: %w", c.Name, err)
	}
	// A clean zero exit is still unexpected for a long-running process.
	return fmt.Errorf("%s exited with status 0", c.Name)
}

// Teardown sends SIGTERM to the running process and waits for it to exit.
// If the process does not exit within StopTimeout, SIGKILL is sent.
func (c *Component) Teardown(ctx context.Context) error {
	c.mu.Lock()
	c.stopping = true
	proc := c.proc
	c.mu.Unlock()

	if proc == nil {
		return nil
	}

	c.log.Info("sending SIGTERM")
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return nil // process already gone
	}

	timeout := c.StopTimeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	poll := time.NewTicker(200 * time.Millisecond)
	defer poll.Stop()

	for {
		select {
		case <-deadline.C:
			c.log.Warn("graceful stop timed out, sending SIGKILL")
			return proc.Kill()
		case <-ctx.Done():
			return proc.Kill()
		case <-poll.C:
			// Signal(0) probes liveness without affecting the process.
			if err := proc.Signal(syscall.Signal(0)); err != nil {
				c.log.Info("process exited")
				return nil
			}
		}
	}
}

func (c *Component) buildEnv() []string {
	var env []string
	if c.EnvInherit {
		for _, e := range os.Environ() {
			if c.EnvPrefix == "" || strings.HasPrefix(e, c.EnvPrefix) {
				env = append(env, e)
			}
		}
	}
	return append(env, c.Env...)
}
