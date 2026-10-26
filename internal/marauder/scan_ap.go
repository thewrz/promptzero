package marauder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	minAPScanDuration = time.Second
	maxAPScanDuration = 60 * time.Second
	maxAPScanResults  = 64
	apCommandTimeout  = 5 * time.Second
)

// ScanAPParsedCtx scans for APs using scanall, then enriches its bounded
// access-point list with info -a replies. duration is clamped to 1–60 seconds.
func (m *Marauder) ScanAPParsedCtx(ctx context.Context, duration time.Duration) (ScanResult, error) {
	return m.scanAPParsed(ctx, duration, nil)
}

// ScanAPParsedStream streams scanall output while sharing the same stop, list,
// and detail workflow as ScanAPParsedCtx. Returning true from onLine stops the
// RF collection early and still returns the collected AP list.
func (m *Marauder) ScanAPParsedStream(ctx context.Context, duration time.Duration, onLine func(line string) (stop bool)) (ScanResult, error) {
	return m.scanAPParsed(ctx, duration, onLine)
}

func (m *Marauder) scanAPParsed(ctx context.Context, duration time.Duration, onLine func(line string) bool) (ScanResult, error) {
	duration = clampAPScanDuration(duration)
	if err := ctx.Err(); err != nil {
		return ScanResult{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.drain()
	if err := ctx.Err(); err != nil {
		return ScanResult{}, err
	}
	result, err := m.collectAPScanLocked(ctx, duration, onLine)
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, fmt.Errorf("scanall canceled: %w", err)
	}
	return m.listAndEnrichAPsLocked(ctx, result)
}

func (m *Marauder) collectAPScanLocked(ctx context.Context, duration time.Duration, onLine func(string) bool) (ScanResult, error) {
	result := ScanResult{}
	command := []byte("scanall\n")
	written, writeErr := m.port.Write(command)
	if writeErr != nil || written != len(command) {
		if writeErr == nil {
			writeErr = io.ErrShortWrite
		}
		body, stopErr := m.stopScanLocked()
		appendRawLines(&result, body, onLine)
		return result, joinScanErrors(fmt.Errorf("sending scanall: %w", writeErr), stopErr)
	}
	stopped := false
	stopScan := func() error {
		if stopped {
			return nil
		}
		stopped = true
		body, err := m.stopScanLocked()
		appendRawLines(&result, body, onLine)
		return err
	}
	return m.collectAPScanOutput(ctx, duration, onLine, &result, stopScan)
}

func (m *Marauder) collectAPScanOutput(ctx context.Context, duration time.Duration, onLine func(string) bool, result *ScanResult, stopScan func() error) (ScanResult, error) {
	startBody, carry, startEcho, err := m.readCommandResponse(ctx, "scanall", apCommandTimeout, nil)
	if err != nil {
		return *result, joinScanErrors(fmt.Errorf("starting scanall: %w", err), stopScan())
	}
	hasStartMessage := strings.Contains(strings.ToLower(startBody), "scanning for aps and stations")
	validStart := hasStartMessage || (startEcho && strings.TrimSpace(startBody) == "")
	if responseHasCommandError(startBody) || !validStart {
		appendRawLines(result, startBody, onLine)
		startErr := fmt.Errorf("scanall returned unexpected response: %q", strings.TrimSpace(startBody))
		return *result, joinScanErrors(startErr, stopScan())
	}
	callbackStopped := appendRawLines(result, startBody, onLine)
	if !callbackStopped {
		if _, err := m.collectScanLines(ctx, duration, carry, result, onLine); err != nil {
			return *result, joinScanErrors(fmt.Errorf("collecting scanall output: %w", err), stopScan())
		}
	}
	if err := stopScan(); err != nil {
		return *result, err
	}
	return *result, nil
}

func (m *Marauder) listAndEnrichAPsLocked(ctx context.Context, result ScanResult) (ScanResult, error) {
	listBody, err := m.execScanCommand(ctx, "list -a", apCommandTimeout)
	if err != nil {
		return result, fmt.Errorf("listing APs after scanall: %w", err)
	}
	if responseHasCommandError(listBody) {
		return result, fmt.Errorf("list -a returned an error: %q", strings.TrimSpace(listBody))
	}
	result.RawLines = append(result.RawLines, splitRawLines(listBody)...)
	parsed, err := parseScanAPList(listBody)
	if err != nil {
		result.RawLines = append(result.RawLines, parsed.RawLines...)
		return result, err
	}
	result.Truncated = parsed.Truncated
	result.Limit = parsed.Limit
	if err := validateAPIndices(parsed.APs); err != nil {
		return result, err
	}
	parsed.APs, err = m.enrichAPsLocked(ctx, parsed.APs, &result)
	if err != nil {
		return result, err
	}
	result.APs = parsed.APs
	result.Count = len(result.APs)
	return result, nil
}

func (m *Marauder) enrichAPsLocked(ctx context.Context, aps []AccessPoint, result *ScanResult) ([]AccessPoint, error) {
	for i := range aps {
		ap := aps[i]
		detailBody, err := m.execScanCommand(ctx, fmt.Sprintf("info -a %d", ap.Index), apCommandTimeout)
		if err != nil {
			return nil, fmt.Errorf("reading info -a %d: %w", ap.Index, err)
		}
		result.RawLines = append(result.RawLines, splitRawLines(detailBody)...)
		if responseHasCommandError(detailBody) {
			return nil, fmt.Errorf("info -a %d returned an error: %q", ap.Index, strings.TrimSpace(detailBody))
		}
		detail, err := parseAPDetail(detailBody)
		if err != nil {
			return nil, fmt.Errorf("parsing info -a %d: %w", ap.Index, err)
		}
		if ap.BSSID != "" && !strings.EqualFold(ap.BSSID, detail.BSSID) {
			return nil, fmt.Errorf("info -a %d BSSID %s does not match list entry %s", ap.Index, detail.BSSID, ap.BSSID)
		}
		ap.SSID = detail.SSID
		ap.BSSID = detail.BSSID
		ap.Channel = detail.Channel
		ap.RSSI = detail.RSSI
		ap.Security = detail.Security
		ap.Hidden = detail.Hidden
		aps[i] = ap
	}
	return aps, nil
}

func clampAPScanDuration(duration time.Duration) time.Duration {
	if duration < minAPScanDuration {
		return minAPScanDuration
	}
	if duration > maxAPScanDuration {
		return maxAPScanDuration
	}
	return duration
}

func (m *Marauder) collectScanLines(ctx context.Context, duration time.Duration, carry []byte, result *ScanResult, onLine func(string) bool) (bool, error) {
	deadline := time.Now().Add(duration)
	buffer := append([]byte(nil), carry...)
	callbackStopped := consumeScanLines(&buffer, result, onLine)
	for !callbackStopped {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		readTimeout := 100 * time.Millisecond
		if remaining < readTimeout {
			readTimeout = remaining
		}
		if err := m.port.SetReadTimeout(readTimeout); err != nil {
			return false, fmt.Errorf("setting scan read timeout: %w", err)
		}
		chunk := make([]byte, 512)
		n, err := m.port.Read(chunk)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return false, fmt.Errorf("Marauder serial port closed during scan")
			}
			return false, fmt.Errorf("reading scanall output: %w", err)
		}
		if n == 0 {
			continue
		}
		buffer = append(buffer, chunk[:n]...)
		callbackStopped = consumeScanLines(&buffer, result, onLine)
		if len(buffer) > m.accumCap() {
			return false, ErrResponseTruncated
		}
	}
	if len(buffer) > 0 {
		dispatchScanLine(string(buffer), result, onLine, &callbackStopped)
	}
	return callbackStopped, nil
}

func consumeScanLines(buffer *[]byte, result *ScanResult, onLine func(string) bool) bool {
	callbackStopped := false
	for {
		index := bytes.IndexByte(*buffer, '\n')
		if index < 0 {
			return callbackStopped
		}
		dispatchScanLine(string((*buffer)[:index]), result, onLine, &callbackStopped)
		*buffer = (*buffer)[index+1:]
	}
}

func dispatchScanLine(raw string, result *ScanResult, onLine func(string) bool, callbackStopped *bool) {
	line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
	if line == "" {
		return
	}
	result.RawLines = append(result.RawLines, line)
	if onLine != nil && !*callbackStopped && onLine(line) {
		*callbackStopped = true
	}
}

func (m *Marauder) stopScanLocked() (string, error) {
	if _, err := m.port.Write([]byte("stopscan\n")); err != nil {
		return "", fmt.Errorf("sending stopscan: %w", err)
	}
	body, _, _, err := m.readCommandResponse(context.Background(), "stopscan", apCommandTimeout, nil)
	if err != nil {
		return body, fmt.Errorf("waiting for stopscan response: %w", err)
	}
	if responseHasCommandError(body) {
		return body, fmt.Errorf("stopscan returned an error: %q", strings.TrimSpace(body))
	}
	return body, nil
}

func (m *Marauder) execScanCommand(ctx context.Context, command string, timeout time.Duration) (string, error) {
	m.drain()
	if _, err := m.port.Write([]byte(command + "\n")); err != nil {
		return "", fmt.Errorf("sending command: %w", err)
	}
	body, _, _, err := m.readCommandResponse(ctx, command, timeout, nil)
	return body, err
}

func (m *Marauder) readCommandResponse(ctx context.Context, command string, timeout time.Duration, pending []byte) (string, []byte, bool, error) {
	readCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	buffer := append([]byte(nil), pending...)
	chunk := make([]byte, 512)
	for {
		if start, end, ok := scanPromptBounds(buffer); ok {
			return parseMarauderResponse(buffer[:start]), append([]byte(nil), buffer[end:]...), responseHasCommandEcho(buffer[:start], command), nil
		}
		if len(buffer) > m.accumCap() {
			return parseMarauderResponse(buffer), nil, false, fmt.Errorf("%s response: %w", command, ErrResponseTruncated)
		}
		if err := readCtx.Err(); err != nil {
			return parseMarauderResponse(buffer), nil, false, fmt.Errorf("%s response: %w", command, err)
		}
		if err := m.port.SetReadTimeout(100 * time.Millisecond); err != nil {
			return parseMarauderResponse(buffer), nil, false, fmt.Errorf("%s set read timeout: %w", command, err)
		}
		n, err := m.port.Read(chunk)
		if err != nil {
			return parseMarauderResponse(buffer), nil, false, fmt.Errorf("%s read: %w", command, err)
		}
		if n > 0 {
			buffer = append(buffer, chunk[:n]...)
		}
	}
}

func responseHasCommandEcho(response []byte, command string) bool {
	lines := strings.Split(strings.ReplaceAll(string(response), "\r\n", "\n"), "\n")
	return len(lines) > 0 && strings.TrimSpace(lines[0]) == "#"+command
}

func scanPromptBounds(buffer []byte) (int, int, bool) {
	for start := 0; start < len(buffer); start++ {
		if buffer[start] != '>' || (start > 0 && buffer[start-1] != '\n') {
			continue
		}
		end := start + 1
		if end < len(buffer) && buffer[end] == ' ' {
			end++
		}
		if end == len(buffer) {
			return start, end, true
		}
		if buffer[end] == '\r' {
			end++
		}
		if end < len(buffer) && buffer[end] == '\n' {
			return start, end + 1, true
		}
	}
	return 0, 0, false
}

func appendRawLines(result *ScanResult, raw string, onLine func(string) bool) bool {
	stopped := false
	for _, line := range splitRawLines(raw) {
		result.RawLines = append(result.RawLines, line)
		if onLine != nil && !stopped && onLine(line) {
			stopped = true
		}
	}
	return stopped
}

func joinScanErrors(primary, cleanup error) error {
	if cleanup == nil {
		return primary
	}
	return errors.Join(primary, fmt.Errorf("cleanup failed: %w", cleanup))
}
