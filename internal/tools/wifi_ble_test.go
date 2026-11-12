package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/xunholy/promptzero/internal/marauder"
	"github.com/xunholy/promptzero/internal/risk"
	"github.com/xunholy/promptzero/internal/tools"
)

var bleScanSpec, bleScanFound = tools.Get("wifi_sniff_bt")
var readCLISpec, readCLIFound = tools.Get("wifi_read_cli")

func TestBLEToolsRegistrationAndHandlers(t *testing.T) {
	for _, tc := range []struct {
		name  string
		spec  tools.Spec
		found bool
		args  map[string]any
	}{
		{"scan", bleScanSpec, bleScanFound, map[string]any{"target_type": "all", "duration_seconds": 1}},
		{"cli", readCLISpec, readCLIFound, map[string]any{"command": "sniffbt", "duration_seconds": 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.found {
				t.Fatal("tool not registered")
			}
			if tc.spec.AgentOnly || tc.spec.Risk != risk.Medium || tc.spec.Group != tools.GroupMarauderWiFi {
				t.Fatal("unexpected tool exposure or risk")
			}
			fp := newTestPort()
			fp.respond("sniffbt", "Name: Test earbuds\n>\n")
			m := marauder.NewWithPort(fp)
			out, err := tc.spec.Handler(context.Background(), &tools.Deps{Marauder: m}, tc.args)
			if err != nil || !strings.Contains(out, "Test earbuds") {
				t.Fatalf("result = %q, %v", out, err)
			}
			if _, err := tc.spec.Handler(context.Background(), &tools.Deps{}, tc.args); err == nil {
				t.Fatal("missing device accepted")
			}
		})
	}
}

func TestBLEToolDurationRejectedBeforeIO(t *testing.T) {
	if !readCLIFound {
		t.Fatal("tool not registered")
	}
	for _, duration := range []any{0, -1, 61, 1.5, "30", 1e100} {
		fp := &bleNoIOPort{t: t}
		m := marauder.NewWithPort(fp)
		_, err := readCLISpec.Handler(context.Background(), &tools.Deps{Marauder: m}, map[string]any{"command": "info", "duration_seconds": duration})
		if err == nil {
			t.Fatalf("invalid duration accepted: %v", duration)
		}
	}
}

// Invalid input must be rejected without touching any serial operation.
type bleNoIOPort struct{ t *testing.T }

func (p *bleNoIOPort) Read([]byte) (int, error) {
	p.t.Error("invalid argument reached serial Read")
	return 0, errors.New("unexpected Read")
}

func (p *bleNoIOPort) Write([]byte) (int, error) {
	p.t.Error("invalid argument reached serial Write")
	return 0, errors.New("unexpected Write")
}

func (p *bleNoIOPort) SetReadTimeout(time.Duration) error {
	p.t.Error("invalid argument reached serial SetReadTimeout")
	return errors.New("unexpected SetReadTimeout")
}

func (*bleNoIOPort) Close() error { return nil }

func TestBLEScanSchemaAdvertisesAll(t *testing.T) {
	var schema struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(bleScanSpec.Schema, &schema); err != nil {
		t.Fatal(err)
	}
	for _, value := range schema.Properties["target_type"].Enum {
		if value == "all" {
			return
		}
	}
	t.Fatal("MCP schema does not advertise all")
}
