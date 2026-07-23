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

func TestCheckTextAllowsLegitimateJoiners(t *testing.T) {
	assert.NoError(t, CheckText("क्\u200dष"))
	assert.NoError(t, CheckText("می\u200cروم"))
}
