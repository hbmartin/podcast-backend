package attest

import (
	"encoding/binary"
	"errors"
)

// authenticatorData is the App Attest / WebAuthn authenticator-data structure
// (docs/AppAttest.md §2.1 step 4). Layout: rpIDHash[32] ‖ flags[1] ‖
// counter[4] ‖ optional attested-credential-data (aaguid[16] ‖ credIDLen[2] ‖
// credID ‖ COSE public key). Attestation carries the attested credential data;
// an assertion carries only the first 37 bytes (though Apple still sets the AT
// flag, so parsing keys off the buffer length, not the flag).
type authenticatorData struct {
	rpIDHash []byte
	flags    byte
	counter  uint32
	aaguid   []byte
	credID   []byte
}

const authDataMinLen = 37

func parseAuthenticatorData(raw []byte) (*authenticatorData, error) {
	if len(raw) < authDataMinLen {
		return nil, errors.New("authenticator data too short")
	}
	ad := &authenticatorData{
		rpIDHash: raw[:32],
		flags:    raw[32],
		counter:  binary.BigEndian.Uint32(raw[33:37]),
	}
	if len(raw) > authDataMinLen {
		if len(raw) < 55 {
			return nil, errors.New("attested credential data truncated")
		}
		ad.aaguid = raw[37:53]
		credIDLen := int(binary.BigEndian.Uint16(raw[53:55]))
		if len(raw) < 55+credIDLen {
			return nil, errors.New("credential id truncated")
		}
		ad.credID = raw[55 : 55+credIDLen]
	}
	return ad, nil
}
