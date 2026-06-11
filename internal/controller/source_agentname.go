package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// defaultAgentName derives the deterministic, collision-safe name of the single
// operator-owned agent for a tunnel (design §3.1). The 8-hex suffix is ALWAYS
// appended and hashed over the UN-sanitized "<ns>/<name>" — sanitization is
// many-to-one, so a length-only hash would let distinct tunnels collide.
func defaultAgentName(tunnelNS, tunnelName string) string {
	sum := sha256.Sum256([]byte(tunnelNS + "/" + tunnelName))
	short := hex.EncodeToString(sum[:4]) // 8 hex chars
	san := sanitizeLabel(tunnelNS + "-" + tunnelName)
	const maxSan = 63 - 1 - 8 // 54
	if len(san) > maxSan {
		san = strings.TrimRight(san[:maxSan], "-")
	}
	if san == "" {
		return short
	}
	return san + "-" + short
}

// sanitizeLabel reduces s to a DNS-1123 label (a-z0-9-), collapsing hyphen runs
// and trimming leading/trailing hyphens. May return "".
func sanitizeLabel(s string) string {
	out := make([]byte, 0, len(s))
	prevHyphen := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			out = append(out, c)
			prevHyphen = false
		case c >= 'A' && c <= 'Z':
			out = append(out, c+32)
			prevHyphen = false
		default:
			if !prevHyphen {
				out = append(out, '-')
				prevHyphen = true
			}
		}
	}
	return strings.Trim(string(out), "-")
}
