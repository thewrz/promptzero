package flipper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

const bridgeUSBResponseTimeout = time.Second

// bridgeHandoffError marks a launch command whose write succeeded but whose
// CLI response could not be confirmed because USB disconnected or stopped
// responding. The caller must verify the peer on the reopened UART before
// treating the handoff as successful.
type bridgeHandoffError struct{ cause error }

func (e bridgeHandoffError) Error() string {
	return fmt.Sprintf("bridge handoff unconfirmed: %v", e.cause)
}

func (e bridgeHandoffError) Unwrap() error                { return e.cause }
func (bridgeHandoffError) BridgeHandoffUnconfirmed() bool { return true }

// launchBridgeUSB sends the configured bridge command once. A missing prompt
// or USB disconnect after the write is treated as an unconfirmed handoff; it
// must never send Ctrl-C or retry because the bridge may already own the UART.
func (f *Flipper) launchBridgeUSB(ctx context.Context, command string) (string, error) {
	if f.bridgeMode.Load() {
		return "", ErrFlipperSuspended
	}
	if f.rpcMode.Load() {
		return "", f.rpcModeError(command)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.bridgeMode.Load() {
		return "", ErrFlipperSuspended
	}
	if f.rpcMode.Load() {
		return "", f.rpcModeError(command)
	}
	if err := f.reconnectIfNeededLocked(ctx); err != nil {
		return "", err
	}
	f.drain()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := f.sendRaw(command + "\r"); err != nil {
		f.markDisconnectedIfRelevant(err)
		return "", fmt.Errorf("sending bridge command: %w", err)
	}

	readCtx, cancel := context.WithTimeout(ctx, bridgeUSBResponseTimeout)
	defer cancel()
	out, err := f.readUntilPromptCtx(readCtx)
	if err == nil {
		return out, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return out, ctxErr
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || isDisconnectError(err) {
		f.markDisconnectedIfRelevant(err)
		if errors.Is(err, io.EOF) {
			f.disconnected.Store(true)
		}
		return out, bridgeHandoffError{cause: err}
	}
	f.markDisconnectedIfRelevant(err)
	return out, err
}
