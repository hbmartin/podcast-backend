// Package attest implements Apple DeviceCheck "App Attest" verification for the
// fork-owned anonymous endpoints (docs/AppAttest.md §2). It verifies attestation
// objects at enrollment (establishing a per-install public key) and per-request
// assertions thereafter. No third-party attestation library is used: the
// verification steps follow Apple's published algorithm against the pinned
// App Attest Root CA in rootca.go.
package attest

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/fxamacker/cbor/v2"
)

// ErrInvalidAttestation marks any client-attributable verification failure
// (bad format, chain, nonce, RP ID, counter, aaguid, or signature). The caller
// maps it to the client's 401 {"errorMessageId":"invalid_attestation"} — never
// use it for a verifier-internal fault, or the client discards a healthy key.
var ErrInvalidAttestation = errors.New("invalid attestation")

const (
	attestationFormat = "apple-appattest"
	// aaguid values (16 bytes) distinguishing the attestation environment.
	aaguidProd = "appattest\x00\x00\x00\x00\x00\x00\x00"
	aaguidDev  = "appattestdevelop"
)

// credCertNonceOID is the App Attest extension carrying the attestation nonce.
var credCertNonceOID = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 8, 2}

// Verifier holds the parsed trust anchor and app-identity policy. Construct one
// with NewVerifier and reuse it; it is safe for concurrent use.
type Verifier struct {
	roots    *x509.CertPool
	appID    string // "TEAMID.bundle.id"
	allowDev bool
	// now is the clock used for certificate-validity checks; overridable in
	// tests to verify against captured (now-expired) sample attestations.
	now func() time.Time
}

// NewVerifier parses the pinned Apple root CA and returns a Verifier for the
// given App ID (TEAMID.bundle.id). allowDev additionally accepts the
// development aaguid (appattestdevelop); production/TestFlight attest under the
// production environment regardless.
func NewVerifier(appID string, allowDev bool) (*Verifier, error) {
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(appleAppAttestRootCAPEM)) {
		return nil, errors.New("attest: failed to parse Apple App Attest root CA")
	}
	return &Verifier{roots: roots, appID: appID, allowDev: allowDev, now: time.Now}, nil
}

type attestationObject struct {
	Fmt      string       `cbor:"fmt"`
	AttStmt  attStatement `cbor:"attStmt"`
	AuthData []byte       `cbor:"authData"`
}

type attStatement struct {
	X5C     [][]byte `cbor:"x5c"`
	Receipt []byte   `cbor:"receipt"`
}

type assertionObject struct {
	Signature         []byte `cbor:"signature"`
	AuthenticatorData []byte `cbor:"authenticatorData"`
}

// VerifyAttestation verifies an enrollment attestation object against the
// server-issued challenge and returns the attested public key (X9.63/SEC1
// uncompressed), the fraud-assessment receipt, and the environment string
// ("production"|"development"). All returned errors that are client-attributable
// wrap ErrInvalidAttestation. Implements docs/AppAttest.md §2.1.
func (v *Verifier) VerifyAttestation(challenge, attestationCBOR, keyID []byte) (pubKey, receipt []byte, environment string, err error) {
	var obj attestationObject
	if err := cbor.Unmarshal(attestationCBOR, &obj); err != nil {
		return nil, nil, "", fmt.Errorf("%w: cbor: %v", ErrInvalidAttestation, err)
	}
	if obj.Fmt != attestationFormat {
		return nil, nil, "", fmt.Errorf("%w: unexpected fmt %q", ErrInvalidAttestation, obj.Fmt)
	}
	if len(obj.AttStmt.X5C) == 0 {
		return nil, nil, "", fmt.Errorf("%w: empty x5c", ErrInvalidAttestation)
	}

	// Step 1: verify the x5c chain (credential cert first) up to the pinned root.
	intermediates := x509.NewCertPool()
	for _, der := range obj.AttStmt.X5C[1:] {
		if c, perr := x509.ParseCertificate(der); perr == nil {
			intermediates.AddCert(c)
		}
	}
	credCert, err := x509.ParseCertificate(obj.AttStmt.X5C[0])
	if err != nil {
		return nil, nil, "", fmt.Errorf("%w: credCert: %v", ErrInvalidAttestation, err)
	}
	if _, err := credCert.Verify(x509.VerifyOptions{
		Roots:         v.roots,
		Intermediates: intermediates,
		CurrentTime:   v.now(),
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}); err != nil {
		return nil, nil, "", fmt.Errorf("%w: chain: %v", ErrInvalidAttestation, err)
	}

	// Steps 2-4: nonce = SHA256(authData ‖ SHA256(challenge)) must equal the
	// octet string in the credCert nonce extension.
	clientDataHash := sha256.Sum256(challenge)
	nonce := sha256.Sum256(append(append([]byte{}, obj.AuthData...), clientDataHash[:]...))
	certNonce, err := extractNonce(credCert)
	if err != nil {
		return nil, nil, "", err
	}
	if subtleTimingSafeUnequal(nonce[:], certNonce) {
		return nil, nil, "", fmt.Errorf("%w: nonce mismatch", ErrInvalidAttestation)
	}

	// Step 5: keyID == SHA256(pubkey), where pubkey is the SEC1 encoding.
	pub, err := ecdsaPubFromCert(credCert)
	if err != nil {
		return nil, nil, "", err
	}
	pubKey, err = marshalSEC1(pub)
	if err != nil {
		return nil, nil, "", err
	}
	pubKeyHash := sha256.Sum256(pubKey)
	if subtleTimingSafeUnequal(pubKeyHash[:], keyID) {
		return nil, nil, "", fmt.Errorf("%w: key id is not SHA256(public key)", ErrInvalidAttestation)
	}

	// Steps 6-9: authenticator data checks (RP ID hash, counter 0, aaguid, credID).
	ad, err := parseAuthenticatorData(obj.AuthData)
	if err != nil {
		return nil, nil, "", fmt.Errorf("%w: authData: %v", ErrInvalidAttestation, err)
	}
	appIDHash := sha256.Sum256([]byte(v.appID))
	if subtleTimingSafeUnequal(ad.rpIDHash, appIDHash[:]) {
		return nil, nil, "", fmt.Errorf("%w: RP ID hash mismatch", ErrInvalidAttestation)
	}
	if ad.counter != 0 {
		return nil, nil, "", fmt.Errorf("%w: attestation counter %d != 0", ErrInvalidAttestation, ad.counter)
	}
	if subtleTimingSafeUnequal(ad.credID, keyID) {
		return nil, nil, "", fmt.Errorf("%w: credential id != key id", ErrInvalidAttestation)
	}
	switch string(ad.aaguid) {
	case aaguidProd:
		environment = "production"
	case aaguidDev:
		if !v.allowDev {
			return nil, nil, "", fmt.Errorf("%w: development aaguid not allowed", ErrInvalidAttestation)
		}
		environment = "development"
	default:
		return nil, nil, "", fmt.Errorf("%w: unexpected aaguid", ErrInvalidAttestation)
	}

	return pubKey, obj.AttStmt.Receipt, environment, nil
}

// VerifyAssertion verifies a per-request assertion (docs/AppAttest.md §2.2)
// over the exact signed body bytes, using the stored SEC1 public key. It
// returns the assertion's counter for the caller's atomic monotonic update;
// it does not itself enforce monotonicity. Client-attributable failures wrap
// ErrInvalidAttestation.
func (v *Verifier) VerifyAssertion(pubKeySEC1, assertionCBOR, signedBody []byte) (counter uint32, err error) {
	var obj assertionObject
	if err := cbor.Unmarshal(assertionCBOR, &obj); err != nil {
		return 0, fmt.Errorf("%w: cbor: %v", ErrInvalidAttestation, err)
	}
	pub, err := parseSEC1(pubKeySEC1)
	if err != nil {
		return 0, err
	}

	// nonce = SHA256(authenticatorData ‖ SHA256(body)); the Secure Enclave signs
	// SHA256(nonce), so the ECDSA digest is a second SHA-256 of the nonce.
	clientDataHash := sha256.Sum256(signedBody)
	nonce := sha256.Sum256(append(append([]byte{}, obj.AuthenticatorData...), clientDataHash[:]...))
	digest := sha256.Sum256(nonce[:])
	if !ecdsa.VerifyASN1(pub, digest[:], obj.Signature) {
		return 0, fmt.Errorf("%w: signature", ErrInvalidAttestation)
	}

	ad, err := parseAuthenticatorData(obj.AuthenticatorData)
	if err != nil {
		return 0, fmt.Errorf("%w: authData: %v", ErrInvalidAttestation, err)
	}
	appIDHash := sha256.Sum256([]byte(v.appID))
	if subtleTimingSafeUnequal(ad.rpIDHash, appIDHash[:]) {
		return 0, fmt.Errorf("%w: RP ID hash mismatch", ErrInvalidAttestation)
	}
	return ad.counter, nil
}

func extractNonce(cert *x509.Certificate) ([]byte, error) {
	for _, ext := range cert.Extensions {
		if !ext.Id.Equal(credCertNonceOID) {
			continue
		}
		var seq []asn1.RawValue
		if _, err := asn1.Unmarshal(ext.Value, &seq); err != nil || len(seq) == 0 {
			return nil, fmt.Errorf("%w: nonce extension malformed", ErrInvalidAttestation)
		}
		var octet asn1.RawValue
		if _, err := asn1.Unmarshal(seq[0].Bytes, &octet); err != nil {
			return nil, fmt.Errorf("%w: nonce octet malformed", ErrInvalidAttestation)
		}
		return octet.Bytes, nil
	}
	return nil, fmt.Errorf("%w: credCert missing nonce extension", ErrInvalidAttestation)
}

func ecdsaPubFromCert(cert *x509.Certificate) (*ecdsa.PublicKey, error) {
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("%w: credCert key is not ECDSA", ErrInvalidAttestation)
	}
	return pub, nil
}

// marshalSEC1 encodes a P-256 public key as the 65-byte uncompressed SEC1/X9.63
// point (0x04 ‖ X ‖ Y), matching the bytes Apple hashes into the key id. Uses
// crypto/ecdh to avoid the deprecated elliptic.Marshal.
func marshalSEC1(pub *ecdsa.PublicKey) ([]byte, error) {
	if pub.Curve != elliptic.P256() {
		return nil, fmt.Errorf("%w: unexpected curve", ErrInvalidAttestation)
	}
	ecdhPub, err := pub.ECDH()
	if err != nil {
		return nil, fmt.Errorf("%w: ecdh: %v", ErrInvalidAttestation, err)
	}
	return ecdhPub.Bytes(), nil
}

// parseSEC1 reconstructs a P-256 ECDSA public key from its SEC1 uncompressed
// encoding, validating the point lies on the curve via crypto/ecdh.
func parseSEC1(b []byte) (*ecdsa.PublicKey, error) {
	if _, err := ecdh.P256().NewPublicKey(b); err != nil {
		return nil, fmt.Errorf("%w: public key: %v", ErrInvalidAttestation, err)
	}
	if len(b) != 65 || b[0] != 0x04 {
		return nil, fmt.Errorf("%w: public key not uncompressed P-256", ErrInvalidAttestation)
	}
	return &ecdsa.PublicKey{
		Curve: elliptic.P256(),
		X:     new(big.Int).SetBytes(b[1:33]),
		Y:     new(big.Int).SetBytes(b[33:65]),
	}, nil
}

// subtleTimingSafeUnequal reports whether a and b differ, using a constant-time
// comparison to avoid leaking where a mismatch occurs.
func subtleTimingSafeUnequal(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) != 1
}
