package marauder

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// blePromptPort models firmware that acknowledges startup with a prompt,
// then sends advertisements asynchronously. Cancellation follows delivery.
type blePromptPort struct {
	*fakePort
	scanning bool
	queued   bool
	cancel   context.CancelFunc
}

func TestBLEScanOutputBoundStillStops(t *testing.T) {
	p := newFakePort()
	p.respond("sniffbt", strings.Repeat("x", 128))
	m := NewWithPort(p)
	m.SetMaxAccumBytes(64)
	_, err := m.SniffBTCtx(context.Background(), "all", time.Second)
	if !errors.Is(err, ErrResponseTruncated) {
		t.Fatalf("oversized output: %v", err)
	}
	if got := strings.Join(p.linesSeen(), "\n"); got != "stopscan\nsniffbt\nstopscan" {
		t.Fatalf("overflow cleanup: %q", got)
	}
}

// An unread startup prompt is still buffered when cancellation happens during
// the command write. It must not masquerade as stopscan acknowledgement.
func TestBLEScanDoesNotAcceptStalePromptAsStopAck(t *testing.T) {
	p := newFakePort()
	p.respond("sniffbt", "Started BLE Scan")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.onWrite = func(command string) {
		if command == "sniffbt" {
			cancel()
		}
	}
	m := NewWithPort(&bleBytePort{p})
	_, err := m.SniffBTCtx(ctx, "all", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	remaining := p.out.String()
	p.mu.Unlock()
	if strings.Contains(remaining, "#stopscan") {
		t.Fatalf("cleanup left stopscan response unread: %q", remaining)
	}
}

type bleBytePort struct{ *fakePort }

func (p *bleBytePort) Read(b []byte) (int, error) {
	return p.fakePort.Read(b[:1])
}

type bleFailedStopPort struct {
	*fakePort
	stops   int
	failure error
}

func (p *bleFailedStopPort) Write(b []byte) (int, error) {
	if string(b) == "stopscan\n" {
		p.stops++
		if p.stops == 2 {
			return 0, p.failure
		}
	}
	return p.fakePort.Write(b)
}

func TestBLEScanReportsCleanupFailure(t *testing.T) {
	failure := errors.New("serial write failed")
	p := &bleFailedStopPort{fakePort: newFakePort(), failure: failure}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.onWrite = func(command string) {
		if command == "sniffbt" {
			cancel()
		}
	}
	m := NewWithPort(p)
	_, err := m.SniffBTCtx(ctx, "all", time.Second)
	if !errors.Is(err, failure) {
		t.Fatalf("unconfirmed stop reported as success: %v", err)
	}
}

func (p *blePromptPort) Write(b []byte) (int, error) {
	p.scanning = strings.HasPrefix(string(b), "sniffbt")
	return p.fakePort.Write(b)
}

func (p *blePromptPort) Read(b []byte) (int, error) {
	n, err := p.fakePort.Read(b)
	if p.scanning && n > 0 {
		if !p.queued {
			p.mu.Lock()
			p.out.WriteString("Name: Late test earbuds, RSSI: -42\r\n")
			p.mu.Unlock()
			p.queued = true
		} else {
			p.cancel()
		}
	}
	return n, err
}

func TestBLEScanContinuesAfterStartupPrompt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &blePromptPort{fakePort: newFakePort(), cancel: cancel}
	p.respond("sniffbt", "Starting Bluetooth scan. Stop with stopscan")
	m := NewWithPort(p)
	t.Cleanup(func() { _ = m.Close() })
	out, err := m.SniffBTCtx(ctx, "all", time.Second)
	if err != nil || !strings.Contains(out, "Late test earbuds") {
		t.Fatalf("advertisements after startup prompt lost: %q, %v", out, err)
	}
	if got := strings.Join(p.linesSeen(), "\n"); got != "stopscan\nsniffbt\nstopscan" {
		t.Fatalf("scan lifecycle = %q; must stop previous mode and clean up", got)
	}
}
