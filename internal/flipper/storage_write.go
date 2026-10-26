package flipper

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// storageWriteReplaceCtx implements the overwrite contract exposed by
// StorageWriteCtx without changing WriteFileCtx's append-capable semantics.
func (f *Flipper) storageWriteReplaceCtx(ctx context.Context, path string, data []byte) error {
	if err := validateStorageWritePath(path); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.IsBLE() {
		// RPC StorageWrite truncates the target on its first frame and
		// appends only continuation frames within the same logical write.
		return f.storageWriteViaRPC(ctx, path, data)
	}

	attempts := f.pipeline().FileWriteRetryAttempts
	if attempts <= 0 {
		attempts = 1
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := f.removeExistingStorageFile(ctx, path); err != nil {
			return err
		}
		err := f.writeStorageFileOnce(ctx, path, data)
		if err == nil {
			return nil
		}
		if attempt == attempts || ctx.Err() != nil || (!errors.Is(err, context.DeadlineExceeded) && !isTransientExecError(err)) {
			return err
		}
		if err := waitForStorageWriteRetry(ctx, f.pipeline().FileWriteRetryDelay); err != nil {
			return err
		}
	}
	return nil
}

func validateStorageWritePath(path string) error {
	if path == "" || path[0] != '/' || sanitizeArg(path) != path || strings.Contains(path, "..") {
		return fmt.Errorf("invalid storage path %q: use an absolute Flipper path without control characters or traversal", path)
	}
	for _, r := range path {
		isAlphanumeric := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if isAlphanumeric || strings.ContainsRune("/_-.", r) {
			continue
		}
		return fmt.Errorf("invalid storage path %q: unsupported character %q", path, r)
	}
	return nil
}

func (f *Flipper) removeExistingStorageFile(ctx context.Context, path string) error {
	exists, isDir, err := f.storageFileExists(ctx, path)
	if err != nil {
		return err
	}
	if isDir {
		return fmt.Errorf("storage write %q: destination is a directory", path)
	}
	if !exists {
		return nil
	}
	out, err := f.ExecCtx(ctx, "storage remove "+sanitizeArg(path))
	if err != nil {
		return fmt.Errorf("storage remove %q: %w", path, err)
	}
	if err := detectFileLoadError(out); err != nil {
		return fmt.Errorf("storage remove %q: %w", path, err)
	}
	return nil
}

func (f *Flipper) storageFileExists(ctx context.Context, path string) (exists bool, isDir bool, err error) {
	out, statErr := f.ExecCtx(ctx, "storage stat "+sanitizeArg(path))
	if statErr != nil {
		return false, false, fmt.Errorf("storage stat %q: %w", path, statErr)
	}
	result := ParseStorageStat(out)
	if result.Exists {
		return true, result.IsDir, nil
	}
	if isStorageNotFound(result.Error) {
		return false, false, nil
	}
	if result.Error != "" {
		return false, false, fmt.Errorf("storage stat %q: %s", path, result.Error)
	}
	return false, false, fmt.Errorf("storage stat %q: unrecognized response %q", path, result.Raw)
}

func isStorageNotFound(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "not found") ||
		strings.Contains(message, "not exist") ||
		strings.Contains(message, "not_exist")
}

func (f *Flipper) writeStorageFileOnce(ctx context.Context, path string, data []byte) error {
	if len(data) == 0 {
		out, err := f.ExecCtx(ctx, "storage write_chunk "+sanitizeArg(path)+" 0")
		if err != nil {
			return err
		}
		return detectFileLoadError(out)
	}
	out, err := f.writeFileOnce(ctx, path, data)
	if err != nil {
		return err
	}
	return detectFileLoadError(out)
}

func waitForStorageWriteRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		delay = 500 * time.Millisecond
	}
	select {
	case <-time.After(delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
