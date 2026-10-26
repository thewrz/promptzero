package flipper

import (
	"context"
	"fmt"
	"time"

	"github.com/xunholy/promptzero/internal/obs"
)

// WriteFile writes data to a file on the Flipper using the storage write_chunk
// interactive protocol: send the command with the byte count, then send the raw
// bytes immediately after. Preserved for backward compatibility; prefer
// WriteFileCtx so cancellation and reconnect propagate a ctx.
func (f *Flipper) WriteFile(path string, data []byte) error {
	return f.WriteFileCtx(context.Background(), path, data)
}

// WriteFileCtx is the context-aware variant of WriteFile. Firmware builds
// that do not truncate an existing file append each chunk; callers requiring
// replacement semantics should use StorageWriteCtx.
func (f *Flipper) WriteFileCtx(ctx context.Context, path string, data []byte) error {
	if f.bridgeMode.Load() {
		return ErrFlipperSuspended
	}
	if f.rpcMode.Load() {
		return f.rpcModeError("write_file " + path)
	}

	pl := f.pipeline()
	attempts := pl.FileWriteRetryAttempts
	if attempts <= 0 {
		attempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		_, lastErr = f.writeFileOnce(ctx, path, data)
		if lastErr == nil {
			return nil
		}
		if !isTransientExecError(lastErr) || ctx.Err() != nil {
			return lastErr
		}
		if attempt < attempts {
			obs.Default().Warn("file_write_retry",
				"path", path,
				"attempt", attempt,
				"max", attempts,
				"err", lastErr)
			delay := pl.FileWriteRetryDelay
			if delay <= 0 {
				delay = 500 * time.Millisecond
			}
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return lastErr
}

func (f *Flipper) writeFileOnce(ctx context.Context, path string, data []byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := f.reconnectIfNeededLocked(ctx); err != nil {
		return "", err
	}

	f.drain()

	cmd := fmt.Sprintf("storage write_chunk %s %d\r", sanitizeArg(path), len(data))
	if err := f.sendRaw(cmd); err != nil {
		f.markDisconnectedIfRelevant(err)
		return "", fmt.Errorf("sending write_chunk command: %w", err)
	}

	select {
	case <-time.After(100 * time.Millisecond):
	case <-ctx.Done():
		return "", ctx.Err()
	}
	f.drain()

	written := 0
	for written < len(data) {
		n, err := f.transport.Write(data[written:])
		if err != nil {
			f.markDisconnectedIfRelevant(err)
			return "", fmt.Errorf("writing file data at offset %d: %w", written, err)
		}
		written += n
	}

	writeTO := f.writeFileTimeout
	if writeTO <= 0 {
		writeTO = f.pipeline().WriteFile
	}
	if writeTO <= 0 {
		writeTO = 10 * time.Second
	}
	readCtx, cancel := context.WithTimeout(ctx, writeTO)
	defer cancel()
	out, err := f.readUntilPromptWithoutEchoCtx(readCtx)
	f.markDisconnectedIfRelevant(err)
	return out, err
}
