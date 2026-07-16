// Package moderation holds the automated pre-filters that run synchronously
// on user-generated text before it becomes publicly visible
// (pocket-casts-ios docs/SocialModeration.md, ADR-0007). This slice ships the
// deterministic checks (length, control characters, UTF-8 validity) plus a
// substring blocklist seam; a real classifier can replace CheckText without
// the handlers changing.
package moderation

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxDisplayNameLen = 60
	MaxBioLen         = 500
)

var (
	ErrEmpty     = errors.New("must not be empty")
	ErrTooLong   = errors.New("too long")
	ErrBadRunes  = errors.New("contains control characters or invalid UTF-8")
	ErrBlocked   = errors.New("contains disallowed content")
	ErrLineBreak = errors.New("must be a single line")
)

// Blocklist is the operator-maintained lowercase substring list applied to
// display names and bios. Deliberately empty in source: populate at deploy
// time (config wiring lands with the operator tooling), never hardcode slurs
// into the repo.
var Blocklist []string

// CheckDisplayName validates a proposed public display name: 1..60 chars,
// single line, printable, blocklist-clean.
func CheckDisplayName(s string) error {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ErrEmpty
	}
	if utf8.RuneCountInString(trimmed) > MaxDisplayNameLen {
		return ErrTooLong
	}
	if strings.ContainsAny(trimmed, "\n\r") {
		return ErrLineBreak
	}
	return CheckText(trimmed)
}

// CheckBio validates a profile bio: 0..500 chars (empty allowed), printable
// (newlines permitted), blocklist-clean.
func CheckBio(s string) error {
	if utf8.RuneCountInString(s) > MaxBioLen {
		return ErrTooLong
	}
	return CheckText(s)
}

// CheckText applies the shared character-level and blocklist checks. Newlines
// and tabs are allowed here; single-line rules belong to the callers.
func CheckText(s string) error {
	if !utf8.ValidString(s) {
		return ErrBadRunes
	}
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' {
			continue
		}
		if unicode.IsControl(r) {
			return ErrBadRunes
		}
	}
	lower := strings.ToLower(s)
	for _, blocked := range Blocklist {
		if blocked != "" && strings.Contains(lower, blocked) {
			return ErrBlocked
		}
	}
	return nil
}
