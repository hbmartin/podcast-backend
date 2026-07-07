package handlers

import (
	"io"
	"net/http"

	"goapi-template/errs"

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
