package marauder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

const scanAllAck = "Scanning for APs and Stations. Stop with stopscan"

const scanAllLines = "-53 Ch: 6 aa:bb:cc:dd:ee:01 ESSID: Example 31 10\n" +
	"STA: aa:bb:cc:dd:ee:10 -> aa:bb:cc:dd:ee:01\n" +
	"-65 Ch: 11 aa:bb:cc:dd:ee:02 ESSID: Other 31 10"

const scanAPList = "[0][CH:6] Example -54\n" +
	"[1][CH:11] aa:bb:cc:dd:ee:02 -70\n" +
	"0 selected"

const exampleDetail = "   ESSID: Example\n" +
	"   BSSID: AA:BB:CC:DD:EE:01\n" +
	" Channel: 6\n" +
	"    RSSI: -54\n" +
	"  Frames: 10\n" +
	"Stations: 4\n" +
	"   Brand: \n" +
	"Complete EAPOL: FALSE\n" +
	"Security: WPA3"

func TestScanAPParsedCtx_CollectsAndEnrichesMarauder1191(t *testing.T) {
	port := newScanScriptPort()
	port.startReply = scanAllAck
	port.scanLines = scanAllLines
	port.listReply = scanAPList
	port.infoReply[0] = exampleDetail
	port.infoReply[1] = strings.ReplaceAll(exampleDetail,
		"ESSID: Example", "ESSID: AA:BB:CC:DD:EE:02")
	port.infoReply[1] = strings.ReplaceAll(port.infoReply[1], "AA:BB:CC:DD:EE:01", "AA:BB:CC:DD:EE:02")
	m := NewWithPort(port)
	t.Cleanup(func() { _ = m.Close() })

	res, err := m.ScanAPParsedCtx(context.Background(), time.Second)
	if err != nil {
		t.Fatalf("ScanAPParsedCtx: %v", err)
	}
	if res.Count != 2 || len(res.APs) != 2 {
		t.Fatalf("scan result has %d/%d APs, want 2/2: %+v", len(res.APs), res.Count, res)
	}
	if got := res.APs[0]; got.SSID != "Example" || got.BSSID != "aa:bb:cc:dd:ee:01" || got.Channel != 6 || got.RSSI != -54 {
		t.Errorf("first AP not enriched from info -a: %+v", got)
	}
	if got := res.APs[1]; got.SSID != "AA:BB:CC:DD:EE:02" || got.BSSID != "aa:bb:cc:dd:ee:02" {
		t.Errorf("second AP evidence was not preserved: %+v", got)
	}
	encoded, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	taps := fields["aps"].([]any)
	first := taps[0].(map[string]any)
	if first["security"] != "WPA3" || first["hidden"] != false {
		t.Errorf("first AP security/hidden JSON = %#v", first)
	}
	second := taps[1].(map[string]any)
	if second["hidden"] != true {
		t.Errorf("SSID equal to BSSID must be marked hidden: %#v", second)
	}
	if !containsExact(res.RawLines, "STA: aa:bb:cc:dd:ee:10 -> aa:bb:cc:dd:ee:01") ||
		!containsExact(res.RawLines, "ESSID: AA:BB:CC:DD:EE:02") {
		t.Errorf("raw scan/detail evidence was not retained: %q", res.RawLines)
	}
	if got, want := port.commands(), []string{"scanall", "stopscan", "list -a", "info -a 0", "info -a 1"}; !sameStrings(got, want) {
		t.Errorf("wire commands = %q, want %q", got, want)
	}
}

func TestScanAPParsedCtx_AcceptsScanallEchoOnlyPrompt(t *testing.T) {
	port := newScanScriptPort()
	port.startReply = "#scanall"
	port.stopReply = "Stopping WiFi tran/recv"
	port.listReply = "[0][CH:6] Example -54\n0 selected"
	port.infoReply[0] = exampleDetail
	m := NewWithPort(port)
	t.Cleanup(func() { _ = m.Close() })

	res, err := m.ScanAPParsedCtx(context.Background(), time.Second)
	if err != nil {
		t.Fatalf("ScanAPParsedCtx with Marauder 1.19.1 echo-only start: %v", err)
	}
	if res.Count != 1 || res.APs[0].SSID != "Example" {
		t.Fatalf("echo-only start result = %+v, want one enriched AP", res)
	}
}

func TestScanAPParsedCtx_RejectsBarePromptAtScanStart(t *testing.T) {
	port := newScanScriptPort()
	port.bareScanPrompt = true
	m := NewWithPort(port)
	t.Cleanup(func() { _ = m.Close() })

	_, err := m.ScanAPParsedCtx(context.Background(), time.Second)
	if err == nil || !strings.Contains(err.Error(), "unexpected response") {
		t.Fatalf("bare scanall prompt error = %v, want unexpected-response error", err)
	}
	if !containsExact(port.commands(), "stopscan") {
		t.Errorf("bare scanall prompt did not trigger stopscan cleanup: %q", port.commands())
	}
}

func TestScanAPParsedCtx_RejectsMalformedEchoResponseAtScanStart(t *testing.T) {
	port := newScanScriptPort()
	port.startReply = "#scanall\nunexpected output"
	m := NewWithPort(port)
	t.Cleanup(func() { _ = m.Close() })

	_, err := m.ScanAPParsedCtx(context.Background(), time.Second)
	if err == nil || !strings.Contains(err.Error(), "unexpected response") {
		t.Fatalf("malformed scanall echo response error = %v, want unexpected-response error", err)
	}
	if !containsExact(port.commands(), "stopscan") {
		t.Errorf("malformed scanall response did not trigger stopscan cleanup: %q", port.commands())
	}
}

func TestScanAPParsedStream_CancelStopsAndReturnsStructuredResults(t *testing.T) {
	port := newScanScriptPort()
	port.startReply = scanAllAck
	port.scanLines = scanAllLines
	port.listReply = "[0][CH:6] Example -54\n0 selected"
	port.infoReply[0] = exampleDetail
	m := NewWithPort(port)
	t.Cleanup(func() { _ = m.Close() })

	res, err := m.ScanAPParsedStream(context.Background(), 60*time.Second, func(line string) bool { return strings.HasPrefix(line, "-") })
	if err != nil {
		t.Fatalf("ScanAPParsedStream after sink cancellation: %v", err)
	}
	if res.Count != 1 || res.APs[0].BSSID != "aa:bb:cc:dd:ee:01" {
		t.Fatalf("cancelled stream should still return enriched AP list: %+v", res)
	}
	if got := port.commands(); !containsExact(got, "stopscan") || !containsExact(got, "list -a") {
		t.Errorf("sink cancellation did not stop and collect: %q", got)
	}
}

func TestScanAPParsedCtx_ContextCancellationStopsScan(t *testing.T) {
	port := newScanScriptPort()
	port.startReply = scanAllAck
	port.scanLines = scanAllLines
	m := NewWithPort(port)
	t.Cleanup(func() { _ = m.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)

	_, err := m.ScanAPParsedCtx(ctx, time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ScanAPParsedCtx error = %v, want context.Canceled", err)
	}
	if got := port.commands(); !containsExact(got, "stopscan") {
		t.Errorf("canceled scan did not send stopscan: %q", got)
	}
}

func TestScanAPParsedCtx_CancelDuringSilentStartupStopsScan(t *testing.T) {
	port := newScanScriptPort()
	port.suppressScanPrompt = true
	m := NewWithPort(port)
	t.Cleanup(func() { _ = m.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	start := time.Now()

	_, err := m.ScanAPParsedCtx(ctx, time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("silent startup cancellation error = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("silent startup cancellation took %s, want prompt bounded cleanup", elapsed)
	}
	if !containsExact(port.commands(), "stopscan") {
		t.Errorf("silent startup cancellation did not send stopscan: %q", port.commands())
	}
}

func TestScanAPParsedCtx_ScanWriteFailureStillAttemptsStop(t *testing.T) {
	port := newScanScriptPort()
	port.failCommand = "scanall"
	m := NewWithPort(port)
	t.Cleanup(func() { _ = m.Close() })

	_, err := m.ScanAPParsedCtx(context.Background(), time.Second)
	if err == nil || !strings.Contains(err.Error(), "scanall") {
		t.Fatalf("scanall write error = %v, want contextual write failure", err)
	}
	if !containsExact(port.commands(), "stopscan") {
		t.Errorf("scanall write failure did not attempt stopscan: %q", port.commands())
	}
}

func TestScanAPParsedCtx_StopFailureIsSurfaced(t *testing.T) {
	port := newScanScriptPort()
	port.startReply = scanAllAck
	port.scanLines = scanAllLines
	port.stopReply = "Error: stopscan failed"
	m := NewWithPort(port)
	t.Cleanup(func() { _ = m.Close() })

	_, err := m.ScanAPParsedStream(context.Background(), time.Second, func(line string) bool { return strings.HasPrefix(line, "-") })
	if err == nil || !strings.Contains(err.Error(), "stopscan") {
		t.Fatalf("stop failure = %v, want contextual stopscan error", err)
	}
	if containsExact(port.commands(), "list -a") {
		t.Error("list -a must not run after stopscan failed")
	}
}

func TestScanAPParsedCtx_RejectsUnsupportedStartAndMalformedDetail(t *testing.T) {
	t.Run("unsupported command", func(t *testing.T) {
		port := newScanScriptPort()
		port.startReply = "Unknown command: scanall"
		m := NewWithPort(port)
		t.Cleanup(func() { _ = m.Close() })
		_, err := m.ScanAPParsedCtx(context.Background(), time.Second)
		if err == nil || !strings.Contains(err.Error(), "scanall") {
			t.Fatalf("unsupported scanall response = %v, want error", err)
		}
		if !containsExact(port.commands(), "stopscan") {
			t.Errorf("scanall start failure must still attempt stopscan: %q", port.commands())
		}
	})

	t.Run("malformed detail", func(t *testing.T) {
		port := newScanScriptPort()
		port.startReply = scanAllAck
		port.listReply = "[0][CH:6] Example -54\n0 selected"
		port.infoReply[0] = "ESSID: Example\nChannel: 6\nRSSI: -54\nSecurity: WPA3"
		m := NewWithPort(port)
		t.Cleanup(func() { _ = m.Close() })
		_, err := m.ScanAPParsedStream(context.Background(), time.Second, func(line string) bool { return strings.HasPrefix(line, "-") })
		if err == nil || !strings.Contains(err.Error(), "info -a 0") {
			t.Fatalf("malformed detail = %v, want contextual parse error", err)
		}
	})
}

func TestScanAPParsedCtx_RejectsIndexOutsideBoundedRange(t *testing.T) {
	port := newScanScriptPort()
	port.startReply = scanAllAck
	port.listReply = "[64][CH:6] Example -54\n0 selected"
	m := NewWithPort(port)
	t.Cleanup(func() { _ = m.Close() })

	_, err := m.ScanAPParsedStream(context.Background(), time.Second, func(line string) bool { return strings.HasPrefix(line, "-") })
	if err == nil || !strings.Contains(err.Error(), "index") {
		t.Fatalf("out-of-range AP index = %v, want bounded-index error", err)
	}
	for _, command := range port.commands() {
		if strings.HasPrefix(command, "info -a ") {
			t.Errorf("out-of-range index triggered detail read %q", command)
		}
	}
}

func TestScanAPParsedCtx_RejectsOverflowedAPIndex(t *testing.T) {
	port := newScanScriptPort()
	port.startReply = scanAllAck
	port.listReply = "[999999999999999999999][CH:6] Example -54\n0 selected"
	m := NewWithPort(port)
	t.Cleanup(func() { _ = m.Close() })

	_, err := m.ScanAPParsedStream(context.Background(), time.Second, func(line string) bool { return strings.HasPrefix(line, "-") })
	if err == nil || !strings.Contains(err.Error(), "list -a") {
		t.Fatalf("overflowed AP index = %v, want bounded-index error", err)
	}
	if containsExact(port.commands(), "info -a 0") {
		t.Error("overflowed AP index must not fall back to index zero")
	}
}

func TestScanAPParsedCtx_CapsAPResultsAndDetailReads(t *testing.T) {
	port := newScanScriptPort()
	port.startReply = scanAllAck
	port.scanLines = "-53 Ch: 6 aa:bb:cc:dd:ee:01 ESSID: Example 31 10"
	var list strings.Builder
	for index := range maxAPScanResults + 1 {
		fmt.Fprintf(&list, "[%d][CH:6] AP-%d -54\n", index, index)
		port.infoReply[index] = fmt.Sprintf("ESSID: AP-%d\nBSSID: AA:BB:CC:DD:EE:01\nChannel: 6\nRSSI: -54\nSecurity: WPA2", index)
	}
	list.WriteString("0 selected")
	port.listReply = list.String()
	m := NewWithPort(port)
	t.Cleanup(func() { _ = m.Close() })

	res, err := m.ScanAPParsedStream(context.Background(), 60*time.Second, func(line string) bool { return strings.HasPrefix(line, "-") })
	if err != nil {
		t.Fatalf("ScanAPParsedStream: %v", err)
	}
	if res.Count != maxAPScanResults || len(res.APs) != maxAPScanResults || !res.Truncated || res.Limit != maxAPScanResults {
		t.Fatalf("capped result = count:%d len:%d truncated:%t limit:%d", res.Count, len(res.APs), res.Truncated, res.Limit)
	}
	commands := port.commands()
	if !containsExact(commands, "info -a 63") || containsExact(commands, "info -a 64") {
		t.Errorf("detail reads crossed AP limit: tail commands %q", commands[len(commands)-4:])
	}
}

func TestClampAPScanDuration(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{name: "minimum", in: 0, want: time.Second},
		{name: "negative", in: -time.Second, want: time.Second},
		{name: "within bound", in: 15 * time.Second, want: 15 * time.Second},
		{name: "maximum", in: 61 * time.Second, want: 60 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := clampAPScanDuration(tc.in); got != tc.want {
				t.Errorf("clampAPScanDuration(%s) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

func TestScanAPParsedCtx_RejectsEmptyOrMismatchedList(t *testing.T) {
	for _, listReply := range []string{"0 selected", "unexpected list response"} {
		t.Run(listReply, func(t *testing.T) {
			port := newScanScriptPort()
			port.startReply = scanAllAck
			port.listReply = listReply
			m := NewWithPort(port)
			t.Cleanup(func() { _ = m.Close() })

			_, err := m.ScanAPParsedStream(context.Background(), time.Second, func(line string) bool { return strings.HasPrefix(line, "-") })
			if err == nil || !strings.Contains(err.Error(), "list -a") {
				t.Fatalf("list response %q returned %v, want parser mismatch error", listReply, err)
			}
		})
	}
}

func TestParseAPList_BracketedMarauderList(t *testing.T) {
	res := ParseAPList(scanAPList)
	if res.Count != 2 || len(res.APs) != 2 {
		t.Fatalf("ParseAPList count = %d, APs=%+v; want 2", res.Count, res.APs)
	}
	if got := res.APs[0]; got.Index != 0 || got.SSID != "Example" || got.Channel != 6 || got.RSSI != -54 {
		t.Errorf("first bracketed AP = %+v", got)
	}
	if got := res.APs[1]; got.Index != 1 || got.SSID != "aa:bb:cc:dd:ee:02" || got.Channel != 11 || got.RSSI != -70 {
		t.Errorf("second bracketed AP = %+v", got)
	}
}

func TestParseAPList_BracketedSSIDKeepsLiteralQuotes(t *testing.T) {
	const line = "[0][CH:6] 'lab' -54"
	res := ParseAPList(line)
	if res.Count != 1 || len(res.APs) != 1 {
		t.Fatalf("ParseAPList count = %d, APs=%+v; want one row", res.Count, res.APs)
	}
	if got := res.APs[0].SSID; got != "'lab'" {
		t.Errorf("SSID = %q, want literal apostrophes retained", got)
	}
	if got := res.APs[0].RawLine; got != line {
		t.Errorf("RawLine = %q, want original line %q", got, line)
	}
}

func TestScanAPParsedStream_InfoPreservesSSIDQuotes(t *testing.T) {
	port := newScanScriptPort()
	port.startReply = scanAllAck
	port.scanLines = "-53 Ch: 6 aa:bb:cc:dd:ee:01 ESSID: 'lab' 31 10"
	port.listReply = "[0][CH:6] 'lab' -54\n0 selected"
	port.infoReply[0] = "ESSID: 'lab'\nBSSID: AA:BB:CC:DD:EE:01\nChannel: 6\nRSSI: -54\nSecurity: WPA2"
	m := NewWithPort(port)
	t.Cleanup(func() { _ = m.Close() })

	res, err := m.ScanAPParsedStream(context.Background(), 60*time.Second, func(line string) bool { return strings.HasPrefix(line, "-") })
	if err != nil {
		t.Fatalf("ScanAPParsedStream: %v", err)
	}
	if res.Count != 1 || res.APs[0].SSID != "'lab'" {
		t.Fatalf("enriched AP SSID = %#v, want literal apostrophes retained", res.APs)
	}
	if res.APs[0].RawLine != "[0][CH:6] 'lab' -54" {
		t.Errorf("list evidence was changed: RawLine = %q", res.APs[0].RawLine)
	}
}

// scanScriptPort models the serial boundary while keeping scan collection
// asynchronous: scanall acknowledges immediately, then emits scan rows.
type scanScriptPort struct {
	mu                 sync.Mutex
	incoming           bytes.Buffer
	outgoing           bytes.Buffer
	commandsSeen       []string
	failCommand        string
	startReply         string
	bareScanPrompt     bool
	scanLines          string
	stopReply          string
	listReply          string
	suppressScanPrompt bool
	infoReply          map[int]string
	readTimeout        time.Duration
	closed             bool
}

func newScanScriptPort() *scanScriptPort {
	return &scanScriptPort{infoReply: make(map[int]string), readTimeout: 100 * time.Millisecond}
}

func (p *scanScriptPort) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, io.EOF
	}
	p.incoming.Write(data)
	for {
		index := bytes.IndexByte(p.incoming.Bytes(), '\n')
		if index < 0 {
			break
		}
		line := strings.TrimSpace(string(p.incoming.Next(index + 1)))
		p.commandsSeen = append(p.commandsSeen, line)
		if line == p.failCommand {
			return 0, errors.New("scripted serial write failure")
		}
		switch {
		case line == "scanall":
			if p.bareScanPrompt {
				p.outgoing.WriteString("> ")
			} else if !p.suppressScanPrompt {
				p.writeReplyLocked(p.startReply)
			}
			if p.scanLines != "" {
				go func(lines string) {
					time.Sleep(5 * time.Millisecond)
					p.push(lines + "\n")
				}(p.scanLines)
			}
		case line == "stopscan":
			p.writeReplyLocked(p.stopReply)
		case line == "list -a":
			p.writeReplyLocked(p.listReply)
		case strings.HasPrefix(line, "info -a "):
			var apIndex int
			_, _ = fmt.Sscanf(line, "info -a %d", &apIndex)
			p.writeReplyLocked(p.infoReply[apIndex])
		default:
			p.writeReplyLocked("Unknown command: " + line)
		}
	}
	return len(data), nil
}

func (p *scanScriptPort) writeReplyLocked(body string) {
	if body != "" {
		p.outgoing.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			p.outgoing.WriteString("\r\n")
		}
	}
	p.outgoing.WriteString("> ")
}

func (p *scanScriptPort) push(body string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.outgoing.WriteString(body)
}

func (p *scanScriptPort) Read(data []byte) (int, error) {
	deadline := time.Now().Add(p.readTimeout)
	for {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return 0, io.EOF
		}
		if p.outgoing.Len() > 0 {
			n, err := p.outgoing.Read(data)
			p.mu.Unlock()
			return n, err
		}
		p.mu.Unlock()
		if time.Now().After(deadline) {
			return 0, nil
		}
		time.Sleep(time.Millisecond)
	}
}

func (p *scanScriptPort) SetReadTimeout(timeout time.Duration) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if timeout > 0 {
		p.readTimeout = timeout
	}
	return nil
}

func (p *scanScriptPort) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	return nil
}

func (p *scanScriptPort) commands() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.commandsSeen...)
}

func containsExact(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
