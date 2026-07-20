package crawler

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// deviceNamespace mirrors the iOS LocalFeedIdentity namespace. Both schemes
// are frozen: the pair of derivations (EpisodeUUID here, DeviceEpisodeUUID
// below) is what makes episode_aliases computable server-side (ADR-0015).
const deviceNamespace = "au.com.pocketcasts.localfeed:"

// DeviceEpisodeUUID replicates the device's local-feed episode identity:
// sha256(namespace + key) shaped like a v5 uuid (version nibble '5',
// RFC-4122 variant nibble). key is the trimmed feed-item guid, falling back
// to the enclosure URL — the same fallback the crawler's own EpisodeUUID key
// uses, so the stored episodes.guid column feeds both derivations.
func DeviceEpisodeUUID(key string) string {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(deviceNamespace + trimmed))
	hexStr := []byte(hex.EncodeToString(digest[:]))[:32]

	hexStr[12] = '5'
	hexStr[16] = variantNibble(hexStr[16])

	return string(hexStr[0:8]) + "-" + string(hexStr[8:12]) + "-" + string(hexStr[12:16]) + "-" +
		string(hexStr[16:20]) + "-" + string(hexStr[20:32])
}

// variantNibble forces the RFC 4122 variant bits (10xx) onto a hex nibble,
// mirroring the iOS implementation exactly.
func variantNibble(c byte) byte {
	value := hexValue(c)
	forced := (value & 0x3) | 0x8
	return "0123456789abcdef"[forced]
}

func hexValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	default:
		return 0
	}
}
