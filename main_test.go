package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProbeURL(t *testing.T) {
	assert.Equal(t, "http://127.0.0.1:8000/health", probeURL("", false), "default port")
	assert.Equal(t, "http://127.0.0.1:8000/health", probeURL(":8000", false))
	assert.Equal(t, "http://127.0.0.1:9000/health", probeURL("0.0.0.0:9000", false), "bind-all host rewritten to loopback")
	assert.Equal(t, "http://127.0.0.1:8000/health", probeURL("localhost:8000", false))
	assert.Equal(t, "http://127.0.0.1:7000/health", probeURL("7000", false), "bare port")
	assert.Equal(t, "https://127.0.0.1:8443/health", probeURL(":8443", true), "TLS-serving instances probed over https")
}
