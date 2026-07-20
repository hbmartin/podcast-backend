package crawler

import "testing"

// The vector is computed independently from the documented device algorithm
// (sha256 of namespace+seed, version nibble '5', RFC-4122 variant nibble) —
// it pins the Go port against the frozen iOS LocalFeedIdentity scheme.
func TestDeviceEpisodeUUIDVector(t *testing.T) {
	got := DeviceEpisodeUUID("guid-1")
	want := "d61839a0-273c-53c8-bd96-9c492126554f"
	if got != want {
		t.Fatalf("DeviceEpisodeUUID vector mismatch: got %s want %s", got, want)
	}
	if DeviceEpisodeUUID("  guid-1  ") != want {
		t.Fatal("device scheme trims its seed")
	}
	if DeviceEpisodeUUID("   ") != "" {
		t.Fatal("blank seeds have no identity")
	}
}
