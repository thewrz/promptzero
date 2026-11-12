package marauder

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ReadCLI executes only explicitly supported diagnostic commands.
func (m *Marauder) ReadCLI(ctx context.Context, command string, timeout time.Duration) (string, error) {
	if err := validateReadDuration(timeout); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	switch command {
	case "help", "info", "stopscan":
		return m.ExecCtx(ctx, command, timeout)
	case "sniffbt":
		return m.SniffBTCtx(ctx, "all", timeout)
	case "sniffbt -t airtag", "sniffbt -t flipper", "sniffbt -t flock", "sniffbt -t meta":
		return m.SniffBTCtx(ctx, strings.TrimPrefix(command, "sniffbt -t "), timeout)
	default:
		return "", fmt.Errorf("unsupported diagnostic command %q; allowed: help, info, stopscan, sniffbt, sniffbt -t airtag/flipper/flock/meta", command)
	}
}

func validateReadDuration(timeout time.Duration) error {
	if timeout <= 0 || timeout > 60*time.Second {
		return fmt.Errorf("duration must be positive and at most 60 seconds")
	}
	return nil
}

// SniffBT collects BLE advertisements, optionally filtered by device type.
func (m *Marauder) SniffBT(targetType string, timeout time.Duration) (string, error) {
	return m.SniffBTCtx(context.Background(), targetType, timeout)
}

// SniffBTCtx stops the scan when its duration expires or ctx is cancelled.
func (m *Marauder) SniffBTCtx(ctx context.Context, targetType string, timeout time.Duration) (string, error) {
	if err := validateReadDuration(timeout); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	command := "sniffbt"
	switch targetType {
	case "all":
	case "airtag", "flipper", "flock", "meta":
		command += " -t " + targetType
	default:
		return "", fmt.Errorf("invalid sniffbt target %q (valid: all, airtag, flipper, flock, meta)", targetType)
	}
	return m.scanBLE(ctx, command, timeout)
}
