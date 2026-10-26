package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/xunholy/promptzero/internal/config"
	"github.com/xunholy/promptzero/internal/flipper"
	"github.com/xunholy/promptzero/internal/marauder"
)

func TestConnectMarauderWithOpsUsesSerialPort(t *testing.T) {
	var gotPort string
	var gotBaud int
	want := &marauder.Marauder{}
	ops := marauderConnectOps{
		serial: func(port string, baud int) (*marauder.Marauder, error) {
			gotPort, gotBaud = port, baud
			return want, nil
		},
	}
	cfg := &config.Config{Marauder: config.MarauderConfig{Port: "/dev/ttyUSB1", BaudRate: 115200}}

	got, err := connectMarauderWithOps(context.Background(), cfg, nil, ops)
	if err != nil {
		t.Fatalf("connectMarauderWithOps() error = %v", err)
	}
	if got != want || gotPort != "/dev/ttyUSB1" || gotBaud != 115200 {
		t.Fatalf("serial result = (%p, %q, %d), want (%p, %q, %d)", got, gotPort, gotBaud, want, "/dev/ttyUSB1", 115200)
	}
}

func TestConnectMarauderWithOpsUsesBLEAddress(t *testing.T) {
	var gotAddress string
	want := &marauder.Marauder{}
	ops := marauderConnectOps{
		ble: func(_ context.Context, address string) (*marauder.Marauder, error) {
			gotAddress = address
			return want, nil
		},
	}
	cfg := &config.Config{Marauder: config.MarauderConfig{Transport: "BLE", Port: "AA:BB:CC:DD:EE:FF"}}

	got, err := connectMarauderWithOps(context.Background(), cfg, nil, ops)
	if err != nil {
		t.Fatalf("connectMarauderWithOps() error = %v", err)
	}
	if got != want || gotAddress != "AA:BB:CC:DD:EE:FF" {
		t.Fatalf("BLE result = (%p, %q), want (%p, %q)", got, gotAddress, want, "AA:BB:CC:DD:EE:FF")
	}
}

func TestConnectMarauderWithOpsUsesFlipperPortForSingleCableBridge(t *testing.T) {
	flip := flipper.NewForTest(flipper.Capabilities{})
	var gotFlip *flipper.Flipper
	var gotPort string
	var gotCommand string
	var gotSettle, gotReopen time.Duration
	want := &marauder.Marauder{}
	ops := marauderConnectOps{
		bridge: func(_ context.Context, f *flipper.Flipper, port string, _ int, command string, settle, reopen time.Duration) (*marauder.Marauder, error) {
			gotFlip, gotPort, gotCommand, gotSettle, gotReopen = f, port, command, settle, reopen
			if err := f.Suspend("UART bridge active"); err != nil {
				return nil, err
			}
			return want, nil
		},
	}
	cfg := &config.Config{
		Serial: config.SerialConfig{Port: "/dev/ttyACM0"},
		Marauder: config.MarauderConfig{
			Bridge: true, Port: "/dev/ignored", BaudRate: 115200,
			BridgeCommand: `loader open "USB-UART Bridge"`,
			BridgeSettle:  750 * time.Millisecond, BridgePortReopenTimeout: 5 * time.Second,
		},
	}

	got, err := connectMarauderWithOps(context.Background(), cfg, flip, ops)
	if err != nil {
		t.Fatalf("connectMarauderWithOps() error = %v", err)
	}
	if got != want || gotFlip != flip || gotPort != "/dev/ttyACM0" || gotCommand != cfg.Marauder.BridgeCommand || gotSettle != cfg.Marauder.BridgeSettle || gotReopen != cfg.Marauder.BridgePortReopenTimeout {
		t.Fatalf("bridge result = (%p, %p, %q, %q, %s, %s)", got, gotFlip, gotPort, gotCommand, gotSettle, gotReopen)
	}
	if !flip.IsSuspended() {
		t.Fatal("successful single-cable bridge did not preserve Flipper suspension")
	}
}

func TestMarauderBridgePortUsesExplicitPortForHybridFlipper(t *testing.T) {
	cfg := &config.Config{
		Serial:   config.SerialConfig{Port: "/dev/ttyACM0"},
		Marauder: config.MarauderConfig{Bridge: true, Port: "/dev/ttyACM1"},
	}
	port, hybrid, err := marauderBridgePort(cfg, "ble")
	if err != nil {
		t.Fatalf("marauderBridgePort() error = %v", err)
	}
	if port != "/dev/ttyACM1" || !hybrid {
		t.Fatalf("marauderBridgePort() = (%q, %t), want (%q, true)", port, hybrid, "/dev/ttyACM1")
	}
}

func TestConnectMarauderWithOpsRejectsSuspendedFlipper(t *testing.T) {
	flip := flipper.NewForTest(flipper.Capabilities{})
	if err := flip.Suspend("UART bridge active"); err != nil {
		t.Fatalf("Suspend() error = %v", err)
	}
	called := false
	ops := marauderConnectOps{bridge: func(context.Context, *flipper.Flipper, string, int, string, time.Duration, time.Duration) (*marauder.Marauder, error) {
		called = true
		return &marauder.Marauder{}, nil
	}}
	cfg := &config.Config{Marauder: config.MarauderConfig{Bridge: true}}

	_, err := connectMarauderWithOps(context.Background(), cfg, flip, ops)
	if !errors.Is(err, flipper.ErrFlipperSuspended) {
		t.Fatalf("connectMarauderWithOps() error = %v, want ErrFlipperSuspended", err)
	}
	if called {
		t.Fatal("bridge connector called for an already suspended Flipper")
	}
	if !flip.IsSuspended() {
		t.Fatal("suspended Flipper state was lost")
	}
}

func TestConnectMarauderForMCPWrapsRequestedWiFiFailure(t *testing.T) {
	wantErr := errors.New("serial device not found")
	ops := marauderConnectOps{serial: func(string, int) (*marauder.Marauder, error) { return nil, wantErr }}
	cfg := &config.Config{Marauder: config.MarauderConfig{Enabled: true, Port: "/dev/ttyUSB9"}}

	_, err := connectMarauderForMCPWithOps(context.Background(), cfg, nil, false, ops)
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "MCP Marauder initialization") {
		t.Fatalf("connectMarauderForMCPWithOps() error = %v, want wrapped serial error", err)
	}
}

func TestConnectMarauderForMCPDoesNotConnectWhenWiFiDisabled(t *testing.T) {
	called := false
	ops := marauderConnectOps{serial: func(string, int) (*marauder.Marauder, error) {
		called = true
		return &marauder.Marauder{}, nil
	}}
	cfg := &config.Config{}

	got, err := connectMarauderForMCPWithOps(context.Background(), cfg, nil, false, ops)
	if err != nil || got != nil || called {
		t.Fatalf("disabled MCP Marauder = (%p, %v), connector called=%t; want nil, nil, false", got, err, called)
	}
}

func TestSetupMarauderKeepsConnectionFailureNonFatal(t *testing.T) {
	wantErr := errors.New("serial device not found")
	ops := marauderConnectOps{serial: func(string, int) (*marauder.Marauder, error) { return nil, wantErr }}
	cfg := &config.Config{Marauder: config.MarauderConfig{Enabled: true}}

	hasMarauder, cleanup := setupMarauderWithOps(context.Background(), cfg, nil, nil, nil, false, ops)
	cleanup()
	if hasMarauder {
		t.Fatal("setupMarauderWithOps() reported a connected Marauder after a connection failure")
	}
}

func TestConnectMarauderWithOpsPreservesBLEConnectionCause(t *testing.T) {
	wantErr := errors.New("BLE adapter unavailable")
	ops := marauderConnectOps{ble: func(context.Context, string) (*marauder.Marauder, error) { return nil, wantErr }}
	cfg := &config.Config{Marauder: config.MarauderConfig{Transport: "ble", Port: "board.local"}}

	_, err := connectMarauderWithOps(context.Background(), cfg, nil, ops)
	if !errors.Is(err, wantErr) {
		t.Fatalf("connectMarauderWithOps() error = %v, want BLE cause", err)
	}
}

func TestConnectMarauderWithOpsPreservesBridgeConnectionCause(t *testing.T) {
	wantErr := errors.New("bridge app rejected launch")
	called := false
	ops := marauderConnectOps{bridge: func(context.Context, *flipper.Flipper, string, int, string, time.Duration, time.Duration) (*marauder.Marauder, error) {
		called = true
		return nil, wantErr
	}}
	cfg := &config.Config{Serial: config.SerialConfig{Port: "/dev/ttyACM0"}, Marauder: config.MarauderConfig{Bridge: true}}

	_, err := connectMarauderWithOps(context.Background(), cfg, flipper.NewForTest(flipper.Capabilities{}), ops)
	if !called || !errors.Is(err, wantErr) {
		t.Fatalf("connectMarauderWithOps() = error %v, bridge called=%t; want wrapped bridge cause", err, called)
	}
}

func TestConnectMarauderWithOpsRejectsMissingClientWithoutError(t *testing.T) {
	ops := marauderConnectOps{serial: func(string, int) (*marauder.Marauder, error) { return nil, nil }}
	cfg := &config.Config{Marauder: config.MarauderConfig{Port: "/dev/ttyUSB1"}}

	got, err := connectMarauderWithOps(context.Background(), cfg, nil, ops)
	if err == nil || got != nil {
		t.Fatalf("connectMarauderWithOps() = (%p, %v), want nil client and error", got, err)
	}
}

func TestMarauderBridgePortRequiresExplicitHybridPort(t *testing.T) {
	cfg := &config.Config{Serial: config.SerialConfig{Port: "/dev/ttyACM0"}, Marauder: config.MarauderConfig{Bridge: true}}
	_, hybrid, err := marauderBridgePort(cfg, "ble")
	if err == nil || !hybrid || !strings.Contains(err.Error(), "requires") {
		t.Fatalf("marauderBridgePort() = (_, %t, %v), want hybrid-port error", hybrid, err)
	}
}

func TestRegisterShutdownCleanupClosesOnceAcrossEOFAndSignal(t *testing.T) {
	var sh signalHandler
	closed := 0
	cleanup := registerShutdownCleanup(&sh, func() { closed++ })

	cleanup()
	sh.runShutdownHooks()
	if closed != 1 {
		t.Fatalf("cleanup calls = %d, want one across normal and signal shutdown", closed)
	}
}
