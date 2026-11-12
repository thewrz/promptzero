package marauder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

func (m *Marauder) writeBLECommand(command string) error {
	n, err := m.port.Write([]byte(command))
	if err == nil && n != len(command) {
		err = io.ErrShortWrite
	}
	return err
}

// stopBLEScan requires m.mu. Discard queued replies before sending stopscan.
// If input never settles, still send the stop but report uncertain cleanup;
// this serial protocol has no request IDs to distinguish old stop replies.
func (m *Marauder) stopBLEScan(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := m.port.SetReadTimeout(100 * time.Millisecond); err != nil {
		return err
	}
	drainErr := m.drainBLEInput(ctx)
	if err := ctx.Err(); err != nil {
		return errors.Join(drainErr, err)
	}
	if err := m.writeBLECommand("stopscan\n"); err != nil {
		return errors.Join(drainErr, err)
	}
	return errors.Join(drainErr, m.readBLEStop(ctx))
}

func (m *Marauder) drainBLEInput(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	buf := make([]byte, 512)
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("settling BLE input: %w", err)
		}
		n, err := m.port.Read(buf)
		if err != nil {
			return fmt.Errorf("draining BLE input: %w", err)
		}
		if n == 0 {
			return nil
		}
	}
}

func (m *Marauder) readBLEStop(ctx context.Context) error {
	var tail []byte
	buf := make([]byte, 512)
	acknowledged := false
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("waiting for stopscan acknowledgement: %w", err)
		}
		n, err := m.port.Read(buf)
		if err != nil {
			return err
		}
		tail = append(tail, buf[:n]...)
		// Ignore startup prompts. NMEA suppresses the echo and emits an end marker.
		if bytes.Contains(tail, []byte("#stopscan")) || bytes.Contains(tail, []byte("END OF NMEA STREAM")) {
			acknowledged = true
		}
		if acknowledged && marauderPromptTail(tail) >= 0 {
			return nil
		}
		if len(tail) > 4096 {
			tail = tail[len(tail)-4096:]
		}
	}
}
