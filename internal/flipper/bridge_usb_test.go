package flipper

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type bridgeTestTransport struct {
	mu       sync.Mutex
	writes   []string
	reads    [][]byte
	response []byte
	readErr  error
	readWait time.Duration
}

func isUnconfirmedBridgeHandoff(err error) bool {
	var marker interface{ BridgeHandoffUnconfirmed() bool }
	return errors.As(err, &marker) && marker.BridgeHandoffUnconfirmed()
}

func (t *bridgeTestTransport) Read(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.reads) > 0 {
		n := copy(p, t.reads[0])
		t.reads = t.reads[1:]
		return n, nil
	}
	if t.readWait > 0 {
		t.mu.Unlock()
		time.Sleep(t.readWait)
		t.mu.Lock()
	}
	return 0, t.readErr
}

func (t *bridgeTestTransport) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.writes = append(t.writes, string(p))
	if len(t.response) > 0 {
		t.reads = append(t.reads, append([]byte(nil), t.response...))
		t.response = nil
	}
	return len(p), nil
}
func (t *bridgeTestTransport) Close() error                       { return nil }
func (t *bridgeTestTransport) Dial(context.Context) error         { return nil }
func (t *bridgeTestTransport) Reconnect(context.Context) error    { return nil }
func (t *bridgeTestTransport) Identity() string                   { return "bridge-test" }
func (t *bridgeTestTransport) DrainTimeout() time.Duration        { return time.Millisecond }
func (t *bridgeTestTransport) Kind() string                       { return "serial" }
func (t *bridgeTestTransport) SetReadTimeout(time.Duration) error { return nil }

func TestLaunchBridgeUSBTimeoutIsUnconfirmedWithoutRetryOrBreak(t *testing.T) {
	tr := &bridgeTestTransport{readWait: time.Millisecond}
	f := &Flipper{transport: tr, execTimeout: 15 * time.Millisecond}

	_, err := f.LaunchBridge(context.Background(), `loader open "USB-UART Bridge"`)
	if err == nil || !isUnconfirmedBridgeHandoff(err) {
		t.Fatalf("LaunchBridge error = %v, want unconfirmed handoff", err)
	}
	if got := strings.Join(tr.writes, ""); got != "loader open \"USB-UART Bridge\"\r" {
		t.Fatalf("wire writes = %q, want exactly one launch command and no break/retry", got)
	}
}

func TestLaunchBridgeUSBReturnsConfirmedResponse(t *testing.T) {
	tr := &bridgeTestTransport{response: []byte("#loader open app\r\nbridge started\r\n>: ")}
	f := &Flipper{transport: tr, execTimeout: time.Second}

	out, err := f.LaunchBridge(context.Background(), "loader open app")
	if err != nil {
		t.Fatalf("LaunchBridge: %v", err)
	}
	if !strings.Contains(out, "bridge started") {
		t.Fatalf("response = %q, want launch output", out)
	}
	if len(tr.writes) != 1 || tr.writes[0] != "loader open app\r" {
		t.Fatalf("writes = %q, want one launch command", tr.writes)
	}
}

func TestLaunchBridgeUSBCancelReturnsContextWithoutBreak(t *testing.T) {
	tr := &bridgeTestTransport{readWait: time.Millisecond}
	f := &Flipper{transport: tr, execTimeout: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := f.LaunchBridge(ctx, "loader open app")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("LaunchBridge error = %v, want context cancellation", err)
	}
	if len(tr.writes) != 0 {
		t.Fatalf("writes = %q, want none after pre-cancel", tr.writes)
	}
}

func TestLaunchBridgeUSBDisconnectIsUnconfirmed(t *testing.T) {
	tr := &bridgeTestTransport{readErr: io.EOF}
	f := &Flipper{transport: tr, execTimeout: time.Second}

	_, err := f.LaunchBridge(context.Background(), "loader open app")
	if err == nil || !isUnconfirmedBridgeHandoff(err) {
		t.Fatalf("LaunchBridge error = %v, want unconfirmed handoff", err)
	}
	if len(tr.writes) != 1 || tr.writes[0] != "loader open app\r" {
		t.Fatalf("writes = %q, want one launch command", tr.writes)
	}
}
