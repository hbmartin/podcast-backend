package moderation

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCheckTextRejectsSpoofingFormatRunes(t *testing.T) {
	for _, text := range []string{
		"safe\u202eevil",
		"safe\u2066evil",
		"safe\u200bevil",
		"safe\ufeffevil",
	} {
		assert.ErrorIs(t, CheckText(text), ErrBadRunes)
	}
}

func TestCheckTextRejectsInvisibleOperatorRunes(t *testing.T) {
	original := Blocklist
	Blocklist = []string{"blockedterm"}
	t.Cleanup(func() { Blocklist = original })

	for r := '\u2061'; r <= '\u2064'; r++ {
		assert.ErrorIs(t, CheckText("blocked"+string(r)+"term"), ErrBadRunes)
	}
	assert.ErrorIs(t, CheckText("blockedterm"), ErrBlocked)
}

func TestCheckTextAllowsLegitimateJoiners(t *testing.T) {
	assert.NoError(t, CheckText("क्\u200dष"))
	assert.NoError(t, CheckText("می\u200cروم"))
}
