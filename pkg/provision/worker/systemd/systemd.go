package systemd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/coreos/go-systemd/v22/dbus"
	"github.com/coreos/go-systemd/v22/unit"
	log "github.com/sirupsen/logrus"
)

type Runner struct {
	conn     *dbus.Conn
	unitName string
}

func NewRunner(ctx context.Context, unitName string) (*Runner, error) {
	conn, err := dbus.NewSystemConnectionContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to systemd: %w", err)
	}
	return &Runner{conn: conn, unitName: unitName}, nil
}
func (r *Runner) Close() {
	r.conn.Close()
}

func (r *Runner) SaveUnit(units []*unit.UnitOption) error {
	content, err := io.ReadAll(unit.Serialize(units))
	if err != nil {
		return fmt.Errorf("failed to serialize %s: %w", r.unitName, err)
	}
	log.WithField("unit", r.unitName).Info("writing unit file")
	if err := os.WriteFile("/etc/systemd/system/"+r.unitName, content, 0644); err != nil {
		return fmt.Errorf("failed to write %s unit file: %w", r.unitName, err)
	}
	return nil
}

func (r *Runner) Run(ctx context.Context) error {
	if err := r.conn.ReloadContext(ctx); err != nil {
		return fmt.Errorf("failed to reload systemd daemon: %w", err)
	}
	if _, _, err := r.conn.EnableUnitFilesContext(ctx, []string{r.unitName}, false, true); err != nil {
		return fmt.Errorf("failed to enable %s: %w", r.unitName, err)
	}

	ch := make(chan string, 1)
	if _, err := r.conn.StartUnitContext(ctx, r.unitName, "replace", ch); err != nil {
		return fmt.Errorf("failed to start %s: %w", r.unitName, err)
	}
	if result := <-ch; result != "done" {
		return fmt.Errorf("failed to start %s: job result %q", r.unitName, result)
	}
	return nil
}
func (r *Runner) Stop(ctx context.Context) error {
	// Stop only if the unit is loaded; avoids an error on a fresh host where
	// the unit file has never been written.
	props, err := r.conn.GetUnitPropertiesContext(ctx, r.unitName)
	if err == nil {
		if loadState, _ := props["LoadState"].(string); loadState != "not-found" {
			log.WithField("unit", r.unitName).Info("stopping unit")
			ch := make(chan string, 1)
			if _, err := r.conn.StopUnitContext(ctx, r.unitName, "replace", ch); err != nil {
				return fmt.Errorf("failed to stop %s: %w", r.unitName, err)
			}
			if result := <-ch; result != "done" {
				return fmt.Errorf("failed to stop %s: job result %q", r.unitName, result)
			}
			log.WithField("unit", r.unitName).Info("unit stopped")
		}
	}

	// DisableUnitFiles is safe to call even when the unit was never enabled.
	if _, err := r.conn.DisableUnitFilesContext(ctx, []string{r.unitName}, false); err != nil {
		return fmt.Errorf("failed to disable %s: %w", r.unitName, err)
	}
	return nil
}

// Cleanup stops and disables the unit, removes its unit file from disk, and
// reloads the systemd daemon so the unit is fully forgotten.
// All errors are collected and returned together via errors.Join.
func (r *Runner) Cleanup(ctx context.Context) error {
	var errs []error

	if err := r.Stop(ctx); err != nil {
		errs = append(errs, err)
	}

	unitFile := "/etc/systemd/system/" + r.unitName
	if err := os.Remove(unitFile); err != nil && !os.IsNotExist(err) {
		errs = append(errs, fmt.Errorf("failed to remove unit file %s: %w", unitFile, err))
	} else {
		log.WithField("unit", r.unitName).Info("unit file removed")
	}

	if err := r.conn.ReloadContext(ctx); err != nil {
		errs = append(errs, fmt.Errorf("failed to reload systemd daemon after cleanup: %w", err))
	}

	return errors.Join(errs...)
}
