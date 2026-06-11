package controller

// Test-only re-export.
// DefaultAgentNameForTest allows external test packages (e.g. test/envtest) to
// compute the deterministic default-agent name without importing unexported symbols.
func DefaultAgentNameForTest(tunnelNS, tunnelName string) string {
	return defaultAgentName(tunnelNS, tunnelName)
}
