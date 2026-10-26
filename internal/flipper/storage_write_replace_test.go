//go:build linux

package flipper_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xunholy/promptzero/internal/flipper"
	"github.com/xunholy/promptzero/internal/flipper/mock"
)

const ofw134DeviceInfo = `hardware_model                : Flipper Zero
hardware_uid                  : 4521480226E18000
hardware_name                 : MockDolphin
firmware_commit               : deadbeef
firmware_origin_fork          : Official
firmware_version              : 1.3.4
firmware_api_major            : 85
firmware_api_minor            : 0
firmware_build_date           : 01-01-2025`

type mockStorage struct {
	mu            sync.Mutex
	files         map[string][]byte
	removeFailure bool
	writeCalls    int
}

func (s *mockStorage) command(args []string) string {
	if len(args) < 2 {
		return "Storage error: invalid parameter"
	}
	path := args[1]
	s.mu.Lock()
	defer s.mu.Unlock()
	switch args[0] {
	case "stat":
		data, ok := s.files[path]
		if !ok {
			return "Storage error: not found"
		}
		return fmt.Sprintf("File, size: %d", len(data))
	case "remove":
		if s.removeFailure {
			return "Storage error: denied"
		}
		delete(s.files, path)
		return ""
	default:
		return "Storage error: unsupported mock command"
	}
}

func (s *mockStorage) write(path string, data []byte, prompt bool) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writeCalls++
	s.files[path] = append(s.files[path], data...)
	return "", prompt
}

func (s *mockStorage) file(path string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.files[path]
	return append([]byte(nil), data...), ok
}

func (s *mockStorage) writes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeCalls
}

func connectOFW134(t *testing.T, opts ...mock.Option) (*flipper.Flipper, *mock.Mock) {
	t.Helper()
	all := []mock.Option{
		mock.WithHandler("device_info", func([]string) string { return ofw134DeviceInfo }),
	}
	all = append(all, opts...)
	m := mock.Spawn(t, all...)
	return connectAndDetect(t, m), m
}

func TestStorageWriteCtxReplacesLongerFile(t *testing.T) {
	const path = "/ext/apps/Scripts/current.js"
	storage := &mockStorage{files: map[string][]byte{path: []byte("the old script is much longer than the new one")}}
	flip, m := connectOFW134(t,
		mock.WithHandler("storage", storage.command),
		mock.WithWriteChunkHandler(func(path string, data []byte) (string, bool) {
			return storage.write(path, data, true)
		}),
	)

	if err := flip.StorageWriteCtx(context.Background(), path, "short"); err != nil {
		t.Fatalf("StorageWriteCtx: %v", err)
	}
	got, ok := storage.file(path)
	if !ok || string(got) != "short" {
		t.Fatalf("file after replacement = %q, exists=%v; want %q", got, ok, "short")
	}

	var operations []string
	for _, line := range m.Lines() {
		if strings.HasPrefix(line, "storage ") {
			operations = append(operations, line)
		}
	}
	want := []string{"storage stat " + path, "storage remove " + path, "storage write_chunk " + path + " 5"}
	if fmt.Sprint(operations) != fmt.Sprint(want) {
		t.Fatalf("storage operations = %v, want %v", operations, want)
	}
}

func TestStorageWriteCtxCreatesMissingFile(t *testing.T) {
	const path = "/ext/apps_data/new.txt"
	storage := &mockStorage{files: map[string][]byte{}}
	flip, m := connectOFW134(t,
		mock.WithHandler("storage", storage.command),
		mock.WithWriteChunkHandler(func(path string, data []byte) (string, bool) {
			return storage.write(path, data, true)
		}),
	)

	if err := flip.StorageWriteCtx(context.Background(), path, "new"); err != nil {
		t.Fatalf("StorageWriteCtx: %v", err)
	}
	got, ok := storage.file(path)
	if !ok || string(got) != "new" {
		t.Fatalf("file after create = %q, exists=%v; want %q", got, ok, "new")
	}
	for _, line := range m.Lines() {
		if strings.HasPrefix(line, "storage remove "+path) {
			t.Fatalf("missing path should not be removed: %v", m.Lines())
		}
	}
}

func TestStorageWriteCtxCreatesEmptyFile(t *testing.T) {
	const path = "/ext/apps_data/empty.txt"
	storage := &mockStorage{files: map[string][]byte{path: []byte("old content")}}
	flip, m := connectOFW134(t,
		mock.WithHandler("storage", storage.command),
		mock.WithWriteChunkHandler(func(path string, data []byte) (string, bool) {
			return storage.write(path, data, true)
		}),
	)

	if err := flip.StorageWriteCtx(context.Background(), path, ""); err != nil {
		t.Fatalf("StorageWriteCtx empty: %v", err)
	}
	got, ok := storage.file(path)
	if !ok || len(got) != 0 {
		t.Fatalf("empty write exists=%v, data=%q; want an empty file", ok, got)
	}
	if !strings.Contains(strings.Join(m.Lines(), "\n"), "storage write_chunk "+path+" 0") {
		t.Fatalf("zero-byte write command missing: %v", m.Lines())
	}
}

func TestStorageWriteCtxStopsOnRemoveFailure(t *testing.T) {
	const path = "/ext/apps_data/locked.txt"
	storage := &mockStorage{files: map[string][]byte{path: []byte("old")}, removeFailure: true}
	flip, m := connectOFW134(t,
		mock.WithHandler("storage", storage.command),
		mock.WithWriteChunkHandler(func(path string, data []byte) (string, bool) {
			return storage.write(path, data, true)
		}),
	)

	err := flip.StorageWriteCtx(context.Background(), path, "new")
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("StorageWriteCtx error = %v, want remove failure", err)
	}
	if got := storage.writes(); got != 0 {
		t.Fatalf("write_chunk called %d times after remove failed", got)
	}
	for _, line := range m.Lines() {
		if strings.HasPrefix(line, "storage write_chunk ") {
			t.Fatalf("write reached transport after failed remove: %v", m.Lines())
		}
	}
}

func TestStorageWriteCtxSurfacesFirmwareWriteFailure(t *testing.T) {
	const path = "/ext/apps_data/read_only.txt"
	storage := &mockStorage{files: map[string][]byte{}}
	flip, _ := connectOFW134(t,
		mock.WithHandler("storage", storage.command),
		mock.WithWriteChunkHandler(func(path string, data []byte) (string, bool) {
			storage.write(path, data, false)
			return "Storage error: denied", true
		}),
	)

	err := flip.StorageWriteCtx(context.Background(), path, "new")
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("StorageWriteCtx error = %v, want firmware write failure", err)
	}
}

func TestStorageWriteCtxRetryReplacesPartialFile(t *testing.T) {
	const path = "/ext/apps_data/retry.txt"
	storage := &mockStorage{files: map[string][]byte{path: []byte("long previous content")}}
	flip, m := connectOFW134(t,
		mock.WithHandler("storage", storage.command),
		mock.WithWriteChunkHandler(func(path string, data []byte) (string, bool) {
			storage.mu.Lock()
			storage.writeCalls++
			storage.files[path] = append(storage.files[path], data...)
			call := storage.writeCalls
			storage.mu.Unlock()
			return "", call > 1
		}),
	)
	flip.SetWriteFileTimeout(40 * time.Millisecond)
	pipeline := flipper.ProfileSettings(flipper.ProfileFast)
	pipeline.FileWriteRetryAttempts = 2
	pipeline.FileWriteRetryDelay = time.Millisecond
	flip.SetPipelineBundle(pipeline)

	if err := flip.StorageWriteCtx(context.Background(), path, "replacement"); err != nil {
		t.Fatalf("StorageWriteCtx with transient write retry: %v", err)
	}
	got, ok := storage.file(path)
	if !ok || string(got) != "replacement" {
		t.Fatalf("file after retry = %q, exists=%v; want %q", got, ok, "replacement")
	}
	if got := storage.writes(); got != 2 {
		t.Fatalf("write calls = %d, want 2", got)
	}
	removeCount := 0
	for _, line := range m.Lines() {
		if strings.HasPrefix(line, "storage remove "+path) {
			removeCount++
		}
	}
	if removeCount != 2 {
		t.Fatalf("remove count = %d, want one before each write attempt; lines=%v", removeCount, m.Lines())
	}
}

func TestStorageWriteCtxValidatesAndHonorsCancellationBeforeSideEffects(t *testing.T) {
	flip, m := connectOFW134(t)
	before := m.Count()
	if err := flip.StorageWriteCtx(context.Background(), "", "data"); err == nil {
		t.Fatal("empty path should be rejected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := flip.StorageWriteCtx(ctx, "/ext/data.txt", "data"); err == nil {
		t.Fatal("cancelled context should be returned")
	}
	if got := m.Count(); got != before {
		t.Fatalf("invalid/cancelled write dispatched %d commands", got-before)
	}
}

func TestJSRunSupportsOfficialFirmware134(t *testing.T) {
	flip, m := connectOFW134(t, mock.WithHandler("js", func([]string) string { return "script done" }))
	if got := flip.Capabilities().JSEngineKind; got != "mjs" {
		t.Fatalf("OFW JS engine = %q, want mjs", got)
	}
	if out, err := flip.JSRun("/ext/apps/Scripts/hello.js", 5*time.Second); err != nil || !strings.Contains(out, "script done") {
		t.Fatalf("JSRun output=%q error=%v", out, err)
	}
	found := false
	for _, line := range m.Lines() {
		if strings.TrimSpace(line) == "js /ext/apps/Scripts/hello.js" {
			found = true
		}
	}
	if !found {
		t.Fatalf("OFW JS command missing; lines=%v", m.Lines())
	}
}
