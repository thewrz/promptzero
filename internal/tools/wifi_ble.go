package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/xunholy/promptzero/internal/risk"
)

//nolint:gochecknoinits
func init() {
	Register(Spec{
		Name:        "wifi_sniff_bt",
		Description: "Collect BLE advertisements through the ESP32 Marauder. Active scanning may send BLE scan requests. Use target_type all for unfiltered discovery, or airtag, flipper, flock, meta. Does not capture established Bluetooth connections. Stops after 1–60 seconds (default 30) or cancellation.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"target_type":{"type":"string","description":"Advertisement filter; all discovers any BLE advertiser.","enum":["all","airtag","flipper","flock","meta"]},"duration_seconds":{"type":"integer","description":"Collection duration in whole seconds, from 1 to 60; default 30.","minimum":1,"maximum":60,"default":30}}}`),
		Required:    []string{"target_type"}, Risk: risk.Medium, Group: GroupMarauderWiFi,
		Handler: func(ctx context.Context, d *Deps, p map[string]any) (string, error) {
			if err := d.RequireMarauder(); err != nil {
				return "", err
			}
			duration, err := bleReadDuration(p)
			if err != nil {
				return "", err
			}
			return d.Marauder.SniffBTCtx(ctx, str(p, "target_type"), duration)
		},
	})
	Register(Spec{
		Name:        "wifi_read_cli",
		Description: "Scoped Marauder diagnostic CLI. Only help, info, stopscan, sniffbt, and sniffbt -t airtag/flipper/flock/meta are accepted. No arbitrary commands, chaining, advertising attacks or configuration changes. Firmware may send BLE scan requests. BLE discovery collects advertisements, not established connections. Duration 1–60 seconds, default 30; scans stop automatically.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","description":"Exact allowlisted Marauder command; extra whitespace or chaining is rejected.","enum":["help","info","stopscan","sniffbt","sniffbt -t airtag","sniffbt -t flipper","sniffbt -t flock","sniffbt -t meta"]},"duration_seconds":{"type":"integer","description":"Command timeout or scan duration in whole seconds, from 1 to 60; default 30.","minimum":1,"maximum":60,"default":30}}}`),
		Required:    []string{"command"}, Risk: risk.Medium, Group: GroupMarauderWiFi,
		Handler: func(ctx context.Context, d *Deps, p map[string]any) (string, error) {
			if err := d.RequireMarauder(); err != nil {
				return "", err
			}
			duration, err := bleReadDuration(p)
			if err != nil {
				return "", err
			}
			return d.Marauder.ReadCLI(ctx, str(p, "command"), duration)
		},
	})
}

func bleReadDuration(p map[string]any) (time.Duration, error) {
	value, exists := p["duration_seconds"]
	if !exists {
		return 30 * time.Second, nil
	}
	var seconds float64
	switch v := value.(type) {
	case int:
		seconds = float64(v)
	case float64:
		seconds = v
	default:
		return 0, fmt.Errorf("duration_seconds must be an integer from 1 to 60")
	}
	if math.IsNaN(seconds) || seconds < 1 || seconds > 60 || math.Trunc(seconds) != seconds {
		return 0, fmt.Errorf("duration_seconds must be an integer from 1 to 60")
	}
	return time.Duration(seconds) * time.Second, nil
}
