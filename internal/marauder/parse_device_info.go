package marauder

import (
	"regexp"
	"strconv"
)

var (
	modernFirmwareRE = regexp.MustCompile(`(?im)^[ \t]*Firmware[ \t]*:[ \t]*Marauder[ \t]*\r?$`)
	modernVersionRE  = regexp.MustCompile(`(?im)^[ \t]*Version[ \t]*:[ \t]*v?([\d.]+)[ \t]*\r?$`)
)

// parseModernDeviceInfo recognizes current firmware's separate key/value
// fields. Both the firmware name and a version are required so a generic
// device's Version field alone cannot identify it as Marauder.
func parseModernDeviceInfo(raw string) (DeviceInfo, bool) {
	if !modernFirmwareRE.MatchString(raw) {
		return DeviceInfo{}, false
	}
	version := modernVersionRE.FindStringSubmatch(raw)
	if len(version) < 2 || version[1] == "" {
		return DeviceInfo{}, false
	}

	d := DeviceInfo{
		FirmwareName:    "ESP32 Marauder",
		FirmwareVersion: version[1],
		Detected:        true,
	}
	if m := reESPIDFVersion.FindStringSubmatch(raw); len(m) > 1 {
		d.ESPIDFVersion = m[1]
	}
	if m := reChannel.FindStringSubmatch(raw); len(m) > 1 {
		d.Channel, _ = strconv.Atoi(m[1])
	}
	if m := reMAC.FindStringSubmatch(raw); len(m) > 1 {
		d.MAC = m[1]
	}
	return d, true
}
