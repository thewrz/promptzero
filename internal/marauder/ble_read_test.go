package marauder

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSniffBTAll(t *testing.T) {
	fp := newFakePort()
	fp.respond("sniffbt", "Name: Test earbuds, RSSI: -40")
	m := newMarauderWithPort(fp)
	t.Cleanup(func() { _ = m.Close() })
	out, err := m.SniffBTCtx(context.Background(), "all", time.Second)
	if err != nil || !strings.Contains(out, "Test earbuds") {
		t.Fatalf("all scan = %q, %v", out, err)
	}
	if got := fp.linesSeen(); len(got) != 3 || got[1] != "sniffbt" {
		t.Fatalf("wire = %q; want bare sniffbt", got)
	}
}

func TestSniffBTStopsAtDeadlineAndCancellation(t *testing.T) {
	for _, cancelScan := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "cancel"}[cancelScan], func(t *testing.T) {
			fp := newFakePort()
			fp.suppressPrompt("sniffbt")
			fp.respond("sniffbt", "Name: Test earbuds")
			fp.respond("stopscan", "Stopped")
			m := newMarauderWithPort(fp)
			t.Cleanup(func() { _ = m.Close() })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if cancelScan {
				fp.onWrite = func(cmd string) {
					if cmd == "sniffbt" {
						cancel()
					}
				}
			}
			_, err := m.SniffBTCtx(ctx, "all", 100*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(fp.linesSeen(), "\n"); got != "stopscan\nsniffbt\nstopscan" {
				t.Fatalf("wire = %q; scan must stop before returning", got)
			}
		})
	}
}

func TestReadCLIAllowlist(t *testing.T) {
	for _, command := range []string{"help", "info", "stopscan", "sniffbt", "sniffbt -t airtag", "sniffbt -t flipper", "sniffbt -t flock", "sniffbt -t meta"} {
		t.Run(command, func(t *testing.T) {
			fp := newFakePort()
			fp.respond(command, "test response")
			m := newMarauderWithPort(fp)
			t.Cleanup(func() { _ = m.Close() })
			out, err := m.ReadCLI(context.Background(), command, 100*time.Millisecond)
			if err != nil || !strings.Contains(out, "test response") {
				t.Fatalf("result = %q, %v", out, err)
			}
			got := fp.linesSeen()
			if strings.HasPrefix(command, "sniffbt") {
				if len(got) != 3 || got[0] != "stopscan" || got[2] != "stopscan" {
					t.Fatalf("scan lifecycle = %q", got)
				}
				got = got[1:2]
			}
			if len(got) != 1 || got[0] != command {
				t.Fatalf("wire = %q", got)
			}
		})
	}
}

func TestReadCLIRejectsBeforeIO(t *testing.T) {
	for _, command := range []string{"", "attack -t deauth", "blespam -t all", "info\nreboot", "info\r", "info;reboot", "info && reboot", "sniffbt -t all", "sniffbt -t apple", "sniffbt\x00", "help\t", " info"} {
		t.Run(command, func(t *testing.T) {
			fp := newFakePort()
			m := newMarauderWithPort(fp)
			_, err := m.ReadCLI(context.Background(), command, time.Second)
			if err == nil || len(fp.linesSeen()) != 0 {
				t.Fatalf("invalid command reached device: %q, %v", fp.linesSeen(), err)
			}
		})
	}
}

func TestBLEReadInvalidDuration(t *testing.T) {
	for _, duration := range []time.Duration{0, -time.Second, 61 * time.Second} {
		fp := newFakePort()
		m := newMarauderWithPort(fp)
		if _, err := m.SniffBTCtx(context.Background(), "all", duration); err == nil {
			t.Errorf("scan accepted %s", duration)
		}
		if _, err := m.ReadCLI(context.Background(), "info", duration); err == nil {
			t.Errorf("CLI accepted %s", duration)
		}
		if len(fp.linesSeen()) != 0 {
			t.Fatalf("invalid duration reached device")
		}
	}
}

// wireBLECmd retains the command spelling checks while asserting the new
// stop/start/stop lifecycle of the BLE wrappers.
func wireBLECmd(t *testing.T, fn func(*Marauder) (string, error)) (string, error) {
	t.Helper()
	fp := newFakePort()
	m := newMarauderWithPort(fp)
	t.Cleanup(func() { _ = m.Close() })
	_, err := fn(m)
	got := fp.linesSeen()
	if len(got) != 3 || got[0] != "stopscan" || got[2] != "stopscan" {
		t.Fatalf("scan lifecycle = %q, %v", got, err)
	}
	return got[1], err
}
