package attest

import "strings"

// Mode is the per-endpoint App Attest enforcement level (docs/AppAttest.md §2.3).
type Mode int

const (
	// ModeOff skips attestation entirely.
	ModeOff Mode = iota
	// ModeLogOnly verifies an assertion when one is present (returning the
	// proper rejection envelopes on invalid material) but accepts unattested
	// requests, logging them. Keeps Simulator/dev/older builds working.
	ModeLogOnly
	// ModeRequired rejects unattested and invalid requests.
	ModeRequired
)

// ParseMode maps a config string to a Mode, defaulting to ModeLogOnly for
// unrecognized non-empty values and to fallback for the empty string.
func ParseMode(s string, fallback Mode) Mode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return fallback
	case "off":
		return ModeOff
	case "required":
		return ModeRequired
	default:
		return ModeLogOnly
	}
}

func (m Mode) String() string {
	switch m {
	case ModeOff:
		return "off"
	case ModeRequired:
		return "required"
	default:
		return "log-only"
	}
}

// Header names carrying the per-request assertion (docs/AppAttest.md §1.3).
const (
	HeaderKeyID     = "X-Attest-Key-Id"
	HeaderAssertion = "X-Attest-Assertion"
)
