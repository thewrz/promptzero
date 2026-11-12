package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/xunholy/promptzero/internal/agent"
	"github.com/xunholy/promptzero/internal/config"
	"github.com/xunholy/promptzero/internal/flipper"
	"github.com/xunholy/promptzero/internal/marauder"
	"github.com/xunholy/promptzero/internal/obs"
)

type marauderConnectOps struct {
	serial      func(string, int) (*marauder.Marauder, error)
	ble         func(context.Context, string) (*marauder.Marauder, error)
	bridge      func(context.Context, *flipper.Flipper, string, int, string, time.Duration, time.Duration, marauder.BridgeOptions) (*marauder.Marauder, error)
	flipperKind func(*flipper.Flipper) string
}

func productionMarauderConnectOps() marauderConnectOps {
	return marauderConnectOps{
		serial:      marauder.Connect,
		ble:         marauder.ConnectBLE,
		flipperKind: transportKind,
		bridge: func(ctx context.Context, flip *flipper.Flipper, port string, baud int, command string, settle, reopen time.Duration, options marauder.BridgeOptions) (*marauder.Marauder, error) {
			return marauder.ConnectViaFlipperWithOptions(ctx, flip, port, baud, command, settle, reopen, options)
		},
	}
}

func registerShutdownCleanup(sh *signalHandler, cleanup func()) func() {
	var once sync.Once
	closeOnce := func() { once.Do(cleanup) }
	if sh != nil {
		sh.AddShutdownHook(closeOnce)
	}
	return closeOnce
}

// connectMarauder chooses the configured Marauder transport and returns its
// connection error to the caller so each startup mode can apply its own policy.
func connectMarauder(ctx context.Context, cfg *config.Config, flip *flipper.Flipper) (*marauder.Marauder, error) {
	return connectMarauderWithOps(ctx, cfg, flip, productionMarauderConnectOps())
}

func connectMarauderWithOps(ctx context.Context, cfg *config.Config, flip *flipper.Flipper, ops marauderConnectOps) (*marauder.Marauder, error) {
	if cfg == nil {
		return nil, errors.New("marauder configuration is nil")
	}
	if cfg.Marauder.Bridge {
		if flip == nil {
			return nil, errors.New("marauder bridge requires a connected Flipper")
		}
		if flip.IsSuspended() {
			return nil, fmt.Errorf("marauder bridge cannot start: %w", flipper.ErrFlipperSuspended)
		}
		kind := transportKind(flip)
		if ops.flipperKind != nil {
			kind = ops.flipperKind(flip)
		}
		port, _, err := marauderBridgePort(cfg, kind)
		if err != nil {
			return nil, err
		}
		m, err := ops.bridge(ctx, flip, port, cfg.Marauder.BaudRate, cfg.Marauder.BridgeCommand, cfg.Marauder.BridgeSettle, cfg.Marauder.BridgePortReopenTimeout, marauder.BridgeOptions{ExitDTR: cfg.Marauder.BridgeExitDTR})
		if err != nil {
			return nil, fmt.Errorf("connecting Marauder via Flipper bridge on %s: %w", port, err)
		}
		if m == nil {
			return nil, errors.New("connecting Marauder via Flipper bridge returned no client")
		}
		if cfg.Marauder.BridgeExitDTR {
			if err := m.EnableBridgeExitDTR(ctx); err != nil {
				armErr := fmt.Errorf("arming custom-FAP bridge exit DTR: %w", err)
				if closeErr := m.Close(); closeErr != nil {
					armErr = errors.Join(armErr, fmt.Errorf("closing Marauder after DTR setup failure: %w", closeErr))
				}
				return nil, armErr
			}
		}
		return m, nil
	}
	if strings.EqualFold(cfg.Marauder.Transport, "ble") {
		if cfg.Marauder.Port == "" {
			return nil, errors.New("marauder BLE address required (set marauder.port or pass --marauder-ble <addr>)")
		}
		m, err := ops.ble(ctx, cfg.Marauder.Port)
		if err != nil {
			return nil, fmt.Errorf("connecting Marauder over BLE at %s: %w", cfg.Marauder.Port, err)
		}
		if m == nil {
			return nil, errors.New("connecting Marauder over BLE returned no client")
		}
		return m, nil
	}
	m, err := ops.serial(cfg.Marauder.Port, cfg.Marauder.BaudRate)
	if err != nil {
		return nil, fmt.Errorf("connecting Marauder on %s: %w", cfg.Marauder.Port, err)
	}
	if m == nil {
		return nil, errors.New("connecting Marauder returned no client")
	}
	return m, nil
}

func transportKind(flip *flipper.Flipper) string {
	if flip == nil || flip.Transport() == nil {
		return ""
	}
	return flip.Transport().Kind()
}

func marauderBridgePort(cfg *config.Config, flipperTransport string) (string, bool, error) {
	if strings.EqualFold(flipperTransport, "ble") {
		if cfg.Marauder.Port == "" {
			return "", true, errors.New("hybrid bridge mode requires an explicit Marauder USB port")
		}
		return cfg.Marauder.Port, true, nil
	}
	return cfg.Serial.Port, false, nil
}

func connectMarauderForMCP(ctx context.Context, cfg *config.Config, flip *flipper.Flipper, wifiEnabled bool) (*marauder.Marauder, error) {
	return connectMarauderForMCPUsing(ctx, cfg, flip, wifiEnabled, connectMarauder)
}

func connectMarauderForMCPWithOps(ctx context.Context, cfg *config.Config, flip *flipper.Flipper, wifiEnabled bool, ops marauderConnectOps) (*marauder.Marauder, error) {
	return connectMarauderForMCPUsing(ctx, cfg, flip, wifiEnabled, func(ctx context.Context, cfg *config.Config, flip *flipper.Flipper) (*marauder.Marauder, error) {
		return connectMarauderWithOps(ctx, cfg, flip, ops)
	})
}

func connectMarauderForMCPUsing(ctx context.Context, cfg *config.Config, flip *flipper.Flipper, wifiEnabled bool, connect func(context.Context, *config.Config, *flipper.Flipper) (*marauder.Marauder, error)) (*marauder.Marauder, error) {
	if cfg == nil || (!wifiEnabled && !cfg.Marauder.Enabled) {
		return nil, nil
	}
	m, err := connect(ctx, cfg, flip)
	if err != nil {
		return nil, fmt.Errorf("MCP Marauder initialization: %w", err)
	}
	return m, nil
}

// setupMarauder attempts the configured Wi-Fi devboard connection. Failures
// stay non-fatal for REPL and web startup, which can continue Flipper-only.
func setupMarauder(ctx context.Context, cfg *config.Config, ai *agent.Agent, rec *obs.Recorder, flip *flipper.Flipper, wifiEnabled bool) (bool, func()) {
	return setupMarauderUsing(ctx, cfg, ai, rec, flip, wifiEnabled, connectMarauder)
}

func setupMarauderWithOps(ctx context.Context, cfg *config.Config, ai *agent.Agent, rec *obs.Recorder, flip *flipper.Flipper, wifiEnabled bool, ops marauderConnectOps) (bool, func()) {
	return setupMarauderUsing(ctx, cfg, ai, rec, flip, wifiEnabled, func(ctx context.Context, cfg *config.Config, flip *flipper.Flipper) (*marauder.Marauder, error) {
		return connectMarauderWithOps(ctx, cfg, flip, ops)
	})
}

func setupMarauderUsing(ctx context.Context, cfg *config.Config, ai *agent.Agent, rec *obs.Recorder, flip *flipper.Flipper, wifiEnabled bool, connect func(context.Context, *config.Config, *flipper.Flipper) (*marauder.Marauder, error)) (bool, func()) {
	if !wifiEnabled && !cfg.Marauder.Enabled {
		return false, func() {}
	}
	if cfg.Marauder.Bridge {
		if port, _, err := marauderBridgePort(cfg, transportKind(flip)); err == nil {
			statusInfo(fmt.Sprintf("Launching Flipper USB-UART Bridge for Marauder on %s%s%s...", bold, port, reset))
		}
	} else if strings.EqualFold(cfg.Marauder.Transport, "ble") {
		if cfg.Marauder.Port != "" {
			statusInfo(fmt.Sprintf("Connecting to Marauder over BLE at %sble://%s%s...", bold, cfg.Marauder.Port, reset))
		}
	} else {
		statusInfo(fmt.Sprintf("Connecting to Marauder on %s%s%s...", bold, cfg.Marauder.Port, reset))
	}
	m, err := connect(ctx, cfg, flip)
	if err != nil {
		switch {
		case cfg.Marauder.Bridge:
			statusWarn(fmt.Sprintf("Marauder bridge: %v", err))
		case strings.EqualFold(cfg.Marauder.Transport, "ble"):
			statusWarn(fmt.Sprintf("Marauder BLE unavailable: %v", err))
		default:
			statusWarn(fmt.Sprintf("Marauder unavailable: %v", err))
		}
		return false, func() {}
	}
	ai.SetMarauder(m)
	if rec != nil {
		rec.SetMarauderConnected(true)
		if cfg.Marauder.Bridge && !strings.EqualFold(transportKind(flip), "ble") {
			rec.SetFlipperConnected(false)
		}
	}
	switch {
	case cfg.Marauder.Bridge:
		port, hybrid, _ := marauderBridgePort(cfg, transportKind(flip))
		if hybrid {
			statusOK(fmt.Sprintf("Marauder via Flipper UART bridge on %s (Flipper CLI on BLE — both active)", port))
		} else {
			statusOK(fmt.Sprintf("Marauder via Flipper UART bridge on %s (Flipper CLI suspended)", port))
			statusWarn("flipper_* tools disabled while UART bridge is active")
		}
	case strings.EqualFold(cfg.Marauder.Transport, "ble"):
		statusOK(fmt.Sprintf("Marauder devboard connected over BLE at %sble://%s%s", bold, cfg.Marauder.Port, reset))
	default:
		statusOK("Marauder WiFi devboard connected")
	}
	return true, func() { _ = m.Close() }
}
