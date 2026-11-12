package marauder

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// A prior Stream cancellation can leave its stop response queued. It cannot
// stand in for either of this scan's stop responses, even on early cancellation.
func TestBLEScanDrainsPriorStopReply(t *testing.T) {
	p := newFakePort()
	p.out.WriteString("#stopscan\r\n> ")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.onWrite = func(command string) {
		if command == "sniffbt" {
			cancel()
		}
	}
	_, err := NewWithPort(&bleBytePort{p}).SniffBTCtx(ctx, "all", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if strings.Contains(p.out.String(), "#stopscan") {
		t.Fatalf("current stop reply remains unread: %q", p.out.String())
	}
}

type bleStartFailurePort struct {
	*fakePort
	partial bool
}

func (p *bleStartFailurePort) Write(b []byte) (int, error) {
	if string(b) != "sniffbt\n" {
		return p.fakePort.Write(b)
	}
	if p.partial {
		return p.fakePort.Write(b[:3])
	}
	n, _ := p.fakePort.Write(b)
	return n, io.ErrUnexpectedEOF
}

func TestBLEScanStartWriteFailureStillStops(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "delivered_with_error", true: "short_write"}[partial], func(t *testing.T) {
			p := &bleStartFailurePort{fakePort: newFakePort(), partial: partial}
			_, err := NewWithPort(p).SniffBTCtx(context.Background(), "all", time.Millisecond)
			want := io.ErrUnexpectedEOF
			if partial {
				want = io.ErrShortWrite
			}
			if !errors.Is(err, want) {
				t.Errorf("start error = %v, want %v", err, want)
			}
			seen := p.linesSeen()
			if len(seen) < 3 || seen[len(seen)-1] != "stopscan" {
				t.Fatalf("failed start did not receive a separate cleanup command: %q", seen)
			}
		})
	}
}

// This models a previous stream flooding input until stopscan is delivered.
// The caller must stop it but cannot claim synchronized input or start a scan.
type bleFloodPort struct {
	*fakePort
	stopped bool
}

func (p *bleFloodPort) Read(b []byte) (int, error) {
	if !p.stopped {
		return copy(b, strings.Repeat("x", len(b))), nil
	}
	return p.fakePort.Read(b)
}

func (p *bleFloodPort) Write(b []byte) (int, error) {
	if string(b) == "stopscan\n" {
		p.stopped = true
	}
	return p.fakePort.Write(b)
}

func TestBLEScanFloodedPreparationFailsClosed(t *testing.T) {
	p := &bleFloodPort{fakePort: newFakePort()}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := NewWithPort(p).SniffBTCtx(ctx, "all", time.Millisecond)
	if err == nil {
		t.Fatal("unsettled input accepted as a synchronized stop")
	}
	if got := strings.Join(p.linesSeen(), "\n"); got != "stopscan" {
		t.Fatalf("preparation must stop the previous stream without starting BLE: %q", got)
	}
}
