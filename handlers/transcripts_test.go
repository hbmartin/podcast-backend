package handlers

import (
	"bytes"
	"compress/gzip"
	"testing"
)

func gz(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestIsTokenFreeURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://example.com/ep1/transcript.vtt", true},
		{"http://example.com/t.srt?lang=en", true},
		{"https://example.com/t.vtt?token=abcdef", false},
		{"https://example.com/t.vtt?sig=x", false},
		{"https://example.com/t.vtt?x=0123456789abcdef", false}, // 16-char value
		{"https://user:pass@example.com/t.vtt", false},
		{"ftp://example.com/t.vtt", false},
		{"not a url", false},
		{"", false},
		{"https://example.com/t.vtt?expires=1", false},
	}
	for _, c := range cases {
		if got := isTokenFreeURL(c.url); got != c.want {
			t.Errorf("isTokenFreeURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestMaxCueEndSeconds(t *testing.T) {
	vtt := []byte("WEBVTT\n\n00:00:01.000 --> 00:00:04.500\nHello\n\n00:01:00.000 --> 01:02:03.250\nWorld\n")
	end, ok := maxCueEndSeconds(vtt)
	if !ok {
		t.Fatal("expected cues to be found")
	}
	want := 1.0*3600 + 2.0*60 + 3.250
	if end < want-0.001 || end > want+0.001 {
		t.Fatalf("maxCueEndSeconds = %v, want %v", end, want)
	}

	if _, ok := maxCueEndSeconds([]byte("WEBVTT\n\nno cues here\n")); ok {
		t.Fatal("expected no cues")
	}
}

func TestParseCueTimestamp(t *testing.T) {
	cases := map[string]float64{
		"00:00:04.500": 4.5,
		"01:02:03.250": 3723.25,
		"02:05.000":    125.0,
		"00:00:00,900": 0.9, // SRT comma
	}
	for in, want := range cases {
		got, ok := parseCueTimestamp(in)
		if !ok || got < want-0.001 || got > want+0.001 {
			t.Errorf("parseCueTimestamp(%q) = %v (ok=%v), want %v", in, got, ok, want)
		}
	}
}

func TestGunzipCapped(t *testing.T) {
	orig := []byte("hello transcript world")
	out, err := gunzipCapped(gz(t, orig), 1024)
	if err != nil || !bytes.Equal(out, orig) {
		t.Fatalf("gunzipCapped round-trip failed: %v", err)
	}
	if _, err := gunzipCapped([]byte("not gzip"), 1024); err == nil {
		t.Fatal("expected error on non-gzip input")
	}
}
