package attest

import (
	"encoding/base64"
	"encoding/hex"
	"testing"
	"time"
)

// fixedApr2021 pins the verifier clock inside the sample credential
// certificate's April-2021 validity window.
func fixedApr2021() time.Time { return time.Date(2021, 4, 15, 12, 0, 0, 0, time.UTC) }

func newSampleVerifier(t *testing.T, allowDev bool) *Verifier {
	t.Helper()
	v, err := NewVerifier(sampleAppID, allowDev)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

func decodeRawURLVector(t *testing.T, name, value string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return b
}

func decodeHexVector(t *testing.T, name, value string) []byte {
	t.Helper()
	b, err := hex.DecodeString(value)
	if err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return b
}

func decodeVectors(t *testing.T) (attObj, keyID, challenge []byte) {
	t.Helper()
	var err error
	if attObj, err = base64.RawURLEncoding.DecodeString(sampleAttObjB64); err != nil {
		t.Fatalf("decode attObj: %v", err)
	}
	if keyID, err = base64.StdEncoding.DecodeString(sampleKeyIDB64); err != nil {
		t.Fatalf("decode keyID: %v", err)
	}
	if challenge, err = base64.RawURLEncoding.DecodeString(sampleClientB64); err != nil {
		t.Fatalf("decode challenge: %v", err)
	}
	return
}

func TestVerifyAttestation_RealVector(t *testing.T) {
	v := newSampleVerifier(t, true)
	v.now = fixedApr2021
	attObj, keyID, challenge := decodeVectors(t)

	pub, receipt, env, err := v.VerifyAttestation(challenge, attObj, keyID)
	if err != nil {
		t.Fatalf("VerifyAttestation: %v", err)
	}
	if got := hex.EncodeToString(pub); got != samplePubKeyHex {
		t.Fatalf("public key mismatch:\n got %s\nwant %s", got, samplePubKeyHex)
	}
	if len(receipt) == 0 {
		t.Fatal("expected a non-empty receipt")
	}
	if env != "development" {
		t.Fatalf("environment = %q, want development", env)
	}
}

func TestVerifyAttestation_RejectsDevWhenNotAllowed(t *testing.T) {
	v := newSampleVerifier(t, false)
	v.now = fixedApr2021
	attObj, keyID, challenge := decodeVectors(t)
	if _, _, _, err := v.VerifyAttestation(challenge, attObj, keyID); err == nil {
		t.Fatal("expected development aaguid to be rejected when allowDev=false")
	}
}

func TestVerifyAttestation_WrongChallenge(t *testing.T) {
	v := newSampleVerifier(t, true)
	v.now = fixedApr2021
	attObj, keyID, _ := decodeVectors(t)
	if _, _, _, err := v.VerifyAttestation([]byte("not-the-challenge"), attObj, keyID); err == nil {
		t.Fatal("expected nonce mismatch for wrong challenge")
	}
}

func TestVerifyAttestation_ExpiredCertRejected(t *testing.T) {
	v := newSampleVerifier(t, true) // default clock = time.Now(), cert expired 2021
	attObj, keyID, challenge := decodeVectors(t)
	if _, _, _, err := v.VerifyAttestation(challenge, attObj, keyID); err == nil {
		t.Fatal("expected expired certificate chain to be rejected under real clock")
	}
}

func TestVerifyAssertion_RealVector(t *testing.T) {
	v := newSampleVerifier(t, true)
	pub := decodeHexVector(t, "public key", samplePubKeyHex)
	signedBody := decodeRawURLVector(t, "assertion client data", sampleAssertClientB64)
	assertion := decodeRawURLVector(t, "assertion", sampleAssertionB64)

	counter, err := v.VerifyAssertion(pub, assertion, signedBody)
	if err != nil {
		t.Fatalf("VerifyAssertion: %v", err)
	}
	if counter == 0 {
		t.Fatalf("counter = 0, want >= 1")
	}
}

func TestVerifyAssertion_TamperedBody(t *testing.T) {
	v := newSampleVerifier(t, true)
	pub := decodeHexVector(t, "public key", samplePubKeyHex)
	assertion := decodeRawURLVector(t, "assertion", sampleAssertionB64)
	if _, err := v.VerifyAssertion(pub, assertion, []byte("tampered body")); err == nil {
		t.Fatal("expected signature failure on tampered body")
	}
}
