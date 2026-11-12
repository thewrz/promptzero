package marauder

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

type bleFaultPort struct {
	*fakePort
	stops      int
	silentStop int
	readError  bool
}

func (p *bleFaultPort) Write(b []byte) (int, error) {
	n, err := p.fakePort.Write(b)
	if string(b) == "stopscan\n" {
		p.stops++
		if p.stops == p.silentStop {
			p.mu.Lock()
			p.out.Reset()
			p.mu.Unlock()
		}
	}
	return n, err
}

func (p *bleFaultPort) Read(b []byte) (int, error) {
	if p.readError && len(p.linesSeen()) == 2 {
		p.readError = false
		return 0, io.ErrUnexpectedEOF
	}
	return p.fakePort.Read(b)
}

func TestBLEScanRequiresStopAcknowledgement(t *testing.T) {
	for _, stop := range []int{1, 2} {
		t.Run(map[int]string{1: "preparation", 2: "cleanup"}[stop], func(t *testing.T) {
			p := &bleFaultPort{fakePort: newFakePort(), silentStop: stop}
			_, err := NewWithPort(p).SniffBTCtx(context.Background(), "all", time.Millisecond)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("unacknowledged stop = %v", err)
			}
			want := "stopscan"
			if stop == 2 {
				want = "stopscan\nsniffbt\nstopscan"
			}
			if got := strings.Join(p.linesSeen(), "\n"); got != want {
				t.Fatalf("wire = %q, want %q", got, want)
			}
		})
	}
}

func TestBLEScanReadFailureStillStops(t *testing.T) {
	p := &bleFaultPort{fakePort: newFakePort(), readError: true}
	_, err := NewWithPort(p).SniffBTCtx(context.Background(), "all", time.Second)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("lost read error: %v", err)
	}
	if got := strings.Join(p.linesSeen(), "\n"); got != "stopscan\nsniffbt\nstopscan" {
		t.Fatalf("read failure left scanner running: %q", got)
	}
}

type bleDelayedReplyPort struct {
	*bleBytePort
	delivered bool
}

func (p *bleDelayedReplyPort) Read(b []byte) (int, error) {
	if !p.delivered {
		p.delivered = true
		// The previous stop's reply arrives while the first read is waiting.
		time.Sleep(20 * time.Millisecond)
		p.mu.Lock()
		p.out.WriteString("#stopscan\r\n> ")
		p.mu.Unlock()
	}
	return p.bleBytePort.Read(b)
}

func TestBLEScanSettlesDelayedPriorReply(t *testing.T) {
	fp := newFakePort()
	p := &bleDelayedReplyPort{bleBytePort: &bleBytePort{fp}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fp.onWrite = func(command string) {
		if command == "sniffbt" {
			cancel()
		}
	}
	_, err := NewWithPort(p).SniffBTCtx(ctx, "all", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	fp.mu.Lock()
	defer fp.mu.Unlock()
	if strings.Contains(fp.out.String(), "#stopscan") {
		t.Fatalf("delayed prior reply hid the current stop reply: %q", fp.out.String())
	}
}
