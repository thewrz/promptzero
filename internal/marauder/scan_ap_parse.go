package marauder

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	apDetailFieldRE = regexp.MustCompile(`(?im)^\s*(ESSID|BSSID|Channel|RSSI|Security)\s*:\s*(.*?)\s*$`)
	apDetailMACRE   = regexp.MustCompile(`(?i)^[0-9a-f]{2}(?::[0-9a-f]{2}){5}$`)
	apListMetaRE    = regexp.MustCompile(`(?i)^\d+\s+selected$`)
	commandErrorRE  = regexp.MustCompile(`(?i)^\s*(?:error\b|unknown command\b|invalid command\b|command not found\b)`)
)

func parseScanAPList(raw string) (ScanResult, error) {
	parsed := ParseAPList(raw)
	if parsed.Count == 0 {
		if len(parsed.RawLines) == 1 && strings.EqualFold(strings.TrimSpace(parsed.RawLines[0]), "0 selected") {
			parsed.APs = []AccessPoint{}
			return parsed, nil
		}
		return parsed, fmt.Errorf("list -a returned no parseable access points: %q", strings.TrimSpace(raw))
	}
	for _, line := range parsed.RawLines {
		if !apListMetaRE.MatchString(line) {
			return parsed, fmt.Errorf("list -a returned unrecognized output: %q", line)
		}
	}
	for _, ap := range parsed.APs {
		if !hasAPListIndex(ap.RawLine) {
			return parsed, fmt.Errorf("list -a AP row has no index: %q", ap.RawLine)
		}
	}
	if len(parsed.APs) > maxAPScanResults {
		parsed.APs = parsed.APs[:maxAPScanResults]
		parsed.Truncated = true
		parsed.Limit = maxAPScanResults
	}
	return parsed, nil
}

func hasAPListIndex(line string) bool {
	return bracketAPIndexRE.MatchString(line) || apIndexRE.MatchString(line)
}

func validateAPIndices(aps []AccessPoint) error {
	seen := make(map[int]struct{}, len(aps))
	for _, ap := range aps {
		if ap.Index >= maxAPScanResults {
			return fmt.Errorf("list -a AP index %d is outside the supported range 0–%d", ap.Index, maxAPScanResults-1)
		}
		if _, ok := seen[ap.Index]; ok {
			return fmt.Errorf("list -a returned duplicate AP index %d", ap.Index)
		}
		seen[ap.Index] = struct{}{}
	}
	return nil
}

func parseAPDetail(raw string) (AccessPoint, error) {
	fields := make(map[string]string, 5)
	for _, match := range apDetailFieldRE.FindAllStringSubmatch(raw, -1) {
		fields[strings.ToLower(match[1])] = strings.TrimSpace(match[2])
	}
	for _, field := range []string{"essid", "bssid", "channel", "rssi", "security"} {
		if _, ok := fields[field]; !ok {
			return AccessPoint{}, fmt.Errorf("missing %s field", strings.ToUpper(field))
		}
	}
	bssid := fields["bssid"]
	if !apDetailMACRE.MatchString(bssid) {
		return AccessPoint{}, fmt.Errorf("invalid BSSID %q", bssid)
	}
	channel, err := strconv.Atoi(fields["channel"])
	if err != nil || channel < 1 {
		return AccessPoint{}, fmt.Errorf("invalid channel %q", fields["channel"])
	}
	rssi, err := strconv.Atoi(fields["rssi"])
	if err != nil {
		return AccessPoint{}, fmt.Errorf("invalid RSSI %q", fields["rssi"])
	}
	ssid := fields["essid"]
	return AccessPoint{
		SSID:     ssid,
		BSSID:    strings.ToLower(bssid),
		Channel:  channel,
		RSSI:     rssi,
		Security: fields["security"],
		Hidden:   ssid == "" || strings.EqualFold(ssid, bssid),
	}, nil
}

func splitRawLines(raw string) []string {
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func responseHasCommandError(raw string) bool {
	for _, line := range splitRawLines(raw) {
		if commandErrorRE.MatchString(line) {
			return true
		}
	}
	return false
}
