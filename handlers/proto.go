package handlers

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"

	"github.com/hbmartin/podcast-backend/errs"

	"google.golang.org/protobuf/proto"
)

// maxProtoBody caps protobuf request bodies; the largest legitimate payloads
// (sync updates with up to 2000 episode records) stay well under this.
const maxProtoBody = 4 << 20

// bindProto reads and unmarshals a protobuf request body. The client sends
// Content-Type: application/octet-stream; no content-type check is enforced.
func bindProto(r *http.Request, m proto.Message) error {
	const op errs.Op = "handlers/bindProto"

	body, err := io.ReadAll(io.LimitReader(r.Body, maxProtoBody))
	if err != nil {
		return errs.E(op, errs.Invalid, err)
	}

	if err := proto.Unmarshal(body, m); err != nil {
		return errs.E(op, errs.Invalid, errs.Code("invalid_protobuf"), err)
	}
	return nil
}

// writeProto marshals m and replies with application/octet-stream, matching
// the wire format the Pocket Casts client expects on api-host endpoints.
func writeProto(w http.ResponseWriter, statusCode int, m proto.Message) {
	body, err := proto.Marshal(m)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(statusCode)
	w.Write(body)
}

// maxDecompressedProto caps the decompressed size of a gzip-encoded protobuf
// body, guarding against decompression bombs independent of the compressed cap.
const maxDecompressedProto = 8 << 20

// bindProtoGzip reads a protobuf request body that the client gzip-encodes
// (Content-Encoding: gzip), as the transcript endpoints do. It falls back to a
// plain read when the header is absent. The compressed body must already be
// size-capped by the caller (the attest middleware enforces the per-endpoint
// cap before this runs).
func bindProtoGzip(r *http.Request, m proto.Message) error {
	const op errs.Op = "handlers/bindProtoGzip"

	var reader io.Reader = r.Body
	if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			return errs.E(op, errs.Invalid, errs.Code("invalid_gzip"), err)
		}
		defer zr.Close()
		reader = zr
	}

	body, err := io.ReadAll(io.LimitReader(reader, maxDecompressedProto))
	if err != nil {
		return errs.E(op, errs.Invalid, err)
	}
	if err := proto.Unmarshal(body, m); err != nil {
		return errs.E(op, errs.Invalid, errs.Code("invalid_protobuf"), err)
	}
	return nil
}

// readCappedBody reads up to max bytes of the request body, replacing r.Body
// with a re-readable buffer for the downstream handler. It returns false and
// writes 413 when the body exceeds the cap. Used by the attest middleware so
// the assertion can sign the exact wire bytes while the handler still decodes
// the body itself.
func readCappedBody(w http.ResponseWriter, r *http.Request, max int64) ([]byte, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, max+1))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return nil, false
	}
	if int64(len(body)) > max {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return nil, false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, true
}
