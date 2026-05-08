package cli

import (
	"crypto/sha256"
	"encoding/hex"
)

// [LAW:types-are-the-program] A two-phase command splits intent (preview) from
// commit (apply). The contract that binds them is a token derived purely from
// the plan — identical plans yield identical tokens, so the protocol is
// stateless: no nonce file, no TTL, no cross-process coordination. If anything
// in the plan drifts between preview and apply, the token mismatches and the
// apply phase refuses by construction.
//
// The token is intentionally short (8 hex chars / 32 bits) — collision risk is
// negligible for human-paced confirmations and the value is comfortable to
// copy from terminal output.
//
// applyToken hashes its parts in order, separated by a NUL byte to prevent
// boundary collisions (e.g. ["a", "bc"] vs ["ab", "c"]).
func applyToken(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum)[:8]
}
