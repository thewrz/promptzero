package marauder

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// scanBLE owns the serial port for the whole scan. Marauder ignores new scan
// commands while already scanning, and its startup prompt does not end BLE
// discovery: advertisements follow asynchronously until stopscan.
func (m *Marauder) scanBLE(ctx context.Context, command string, duration time.Duration) (output string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := m.stopBLEScan(ctx); err != nil {
		return "", fmt.Errorf("preparing BLE scan: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	resetLine := false
	defer func() {
		// Cleanup needs its own budget even when the caller has cancelled.
		// A failed write may leave a partial command in the firmware parser.
		if resetLine {
			if resetErr := m.writeBLECommand("\n"); resetErr != nil {
				err = errors.Join(err, fmt.Errorf("resetting BLE command line: %w", resetErr))
			}
		}
		if stopErr := m.stopBLEScan(context.Background()); stopErr != nil {
			err = errors.Join(err, fmt.Errorf("stopping BLE scan: %w", stopErr))
		}
	}()
	if err := m.writeBLECommand(command + "\n"); err != nil {
		resetLine = true
		return "", fmt.Errorf("starting BLE scan: %w", err)
	}
	return m.readBLEScan(ctx, duration)
}

func (m *Marauder) readBLEScan(ctx context.Context, duration time.Duration) (string, error) {
	scanCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	if err := m.port.SetReadTimeout(100 * time.Millisecond); err != nil {
		return "", fmt.Errorf("setting BLE read timeout: %w", err)
	}
	var data []byte
	buf := make([]byte, 512)
	for {
		if scanCtx.Err() != nil {
			return bleScanOutput(data), nil
		}
		n, readErr := m.port.Read(buf)
		data = append(data, buf[:n]...)
		if len(data) > m.accumCap() {
			return bleScanOutput(data[:m.accumCap()]), ErrResponseTruncated
		}
		if readErr != nil {
			return bleScanOutput(data), fmt.Errorf("reading BLE scan: %w", readErr)
		}
	}
}

func bleScanOutput(data []byte) string {
	lines := strings.Split(parseMarauderResponse(data), "\n")
	var output []string
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimPrefix(line, ">"))
		if line != "" {
			output = append(output, line)
		}
	}
	return strings.Join(output, "\n")
}
