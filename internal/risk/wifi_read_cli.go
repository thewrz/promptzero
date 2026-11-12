package risk

//nolint:gochecknoinits
func init() {
	// Match wifi_sniff_bt, the highest-risk operation in the fixed allowlist.
	toolLevels["wifi_read_cli"] = Medium
}
