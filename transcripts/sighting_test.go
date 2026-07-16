package transcripts

import (
	"net"
	"testing"
)

func TestBlockedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "::1", // loopback
		"10.0.0.5", "192.168.1.1", "172.16.0.1", // RFC1918
		"169.254.169.254", // cloud metadata (link-local)
		"fe80::1",         // IPv6 link-local
		"fd00::1",         // IPv6 ULA (private)
		"0.0.0.0", "::",   // unspecified
		"224.0.0.1", // multicast
	}
	for _, s := range blocked {
		if !blockedIP(net.ParseIP(s)) {
			t.Errorf("expected %s to be blocked", s)
		}
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:2800:220:1:248:1893:25c8:1946"}
	for _, s := range allowed {
		if blockedIP(net.ParseIP(s)) {
			t.Errorf("expected %s to be allowed", s)
		}
	}
	if !blockedIP(nil) {
		t.Error("nil IP should be blocked")
	}
}
