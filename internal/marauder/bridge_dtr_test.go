package marauder

import (
	"context"
	"errors"
	"io"
	"reflect"
	"sync"
	"testing"
	"time"
)

type dtrLifecyclePort struct {
	mu        sync.Mutex
	events    []string
	setErrors map[bool]error
	closeErr  error
}

func (p *dtrLifecyclePort) Read([]byte) (int, error)           { return 0, io.EOF }
func (p *dtrLifecyclePort) Write(b []byte) (int, error)        { return len(b), nil }
func (p *dtrLifecyclePort) SetReadTimeout(time.Duration) error { return nil }

func (p *dtrLifecyclePort) SetDTR(high bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if high {
		p.events = append(p.events, "dtr-high")
	} else {
		p.events = append(p.events, "dtr-low")
	}
	return p.setErrors[high]
}

func (p *dtrLifecyclePort) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, "port-close")
	return p.closeErr
}

func (p *dtrLifecyclePort) recordedEvents() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.events...)
}

func TestEnableBridgeExitDTRDwellAndCloseOrder(t *testing.T) {
	dtrCloseErr := errors.New("lowering DTR failed")
	portCloseErr := errors.New("closing serial port failed")
	p := &dtrLifecyclePort{setErrors: map[bool]error{false: dtrCloseErr}, closeErr: portCloseErr}
	m := NewWithPort(p)
	started := time.Now()

	if err := m.EnableBridgeExitDTR(context.Background()); err != nil {
		t.Fatalf("EnableBridgeExitDTR() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed < 500*time.Millisecond {
		t.Fatalf("DTR high dwell = %s, want at least 500ms", elapsed)
	}
	if got := p.recordedEvents(); !reflect.DeepEqual(got, []string{"dtr-high"}) {
		t.Fatalf("events after arming = %v, want [dtr-high]", got)
	}

	closeErr := m.Close()
	if !errors.Is(closeErr, dtrCloseErr) || !errors.Is(closeErr, portCloseErr) {
		t.Fatalf("Close() error = %v, want joined DTR and port errors", closeErr)
	}
	if got, want := p.recordedEvents(), []string{"dtr-high", "dtr-low", "port-close"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("close events = %v, want %v", got, want)
	}
	if err := m.Close(); !errors.Is(err, dtrCloseErr) || !errors.Is(err, portCloseErr) {
		t.Fatalf("second Close() error = %v, want the original joined error", err)
	}
	if got, want := p.recordedEvents(), []string{"dtr-high", "dtr-low", "port-close"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events after second close = %v, want %v", got, want)
	}
}

func TestEnableBridgeExitDTRCancellationCanBeClosed(t *testing.T) {
	p := &dtrLifecyclePort{setErrors: map[bool]error{}}
	m := NewWithPort(p)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := m.EnableBridgeExitDTR(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("EnableBridgeExitDTR() error = %v, want context deadline", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close() after cancelled arming error = %v", err)
	}
	if got, want := p.recordedEvents(), []string{"dtr-high", "dtr-low", "port-close"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func TestEnableBridgeExitDTRReturnsHighErrorAndCloseLowersLine(t *testing.T) {
	setErr := errors.New("asserting DTR failed")
	p := &dtrLifecyclePort{setErrors: map[bool]error{true: setErr}}
	m := NewWithPort(p)

	err := m.EnableBridgeExitDTR(context.Background())
	if !errors.Is(err, setErr) {
		t.Fatalf("EnableBridgeExitDTR() error = %v, want DTR assertion cause", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close() after DTR assertion error = %v", err)
	}
	if got, want := p.recordedEvents(), []string{"dtr-high", "dtr-low", "port-close"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func TestEnableBridgeExitDTRRequiresOptionalPortCapability(t *testing.T) {
	p := newFakePort()
	m := NewWithPort(p)

	if err := m.EnableBridgeExitDTR(context.Background()); err == nil {
		t.Fatal("EnableBridgeExitDTR() error = nil, want unsupported-port error")
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close() on ordinary port = %v", err)
	}
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if !closed {
		t.Fatal("ordinary port was not closed after unsupported DTR request")
	}
}
