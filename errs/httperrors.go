package errs

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
)

// ErrResponse is used as the response body for any error returned to a client.
type ErrResponse struct {
	Error ServiceError `json:"error"`
}

// ServiceError is the client-safe representation of an Error.
type ServiceError struct {
	Kind    string `json:"kind,omitempty"`
	Code    string `json:"code,omitempty"`
	Param   string `json:"param,omitempty"`
	Message string `json:"message,omitempty"`
}

// HTTPErrorResponse takes a writer and an error, logs the error with its
// operation stack trace and sends a client-safe JSON error response.
// Raw database and internal error details are masked so they never leak
// to clients.
func HTTPErrorResponse(w http.ResponseWriter, err error) {
	if err == nil {
		slog.Error("nil error passed to HTTPErrorResponse")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	var e *Error
	if !errors.As(err, &e) {
		// Unclassified native error (e.g. system panic)
		slog.Error("Unclassified system error", "error", err)
		sendJSON(w, http.StatusInternalServerError, ErrResponse{
			Error: ServiceError{
				Kind:    "unanticipated_error",
				Message: "Unexpected internal server error.",
			},
		})
		return
	}

	statusCode := httpStatusCode(e.Kind)
	ops := OpStack(e)

	// Log with operation stack trace and context fields
	slog.Error("API execution failed",
		"http_statuscode", statusCode,
		"kind", e.Kind.String(),
		"code", string(e.Code),
		"parameter", string(e.Param),
		"op_stack", ops,
		"error", e.Error(),
	)

	// Send formatted response
	var res ErrResponse
	switch e.Kind {
	case Unauthenticated:
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s"`, e.Realm))
		w.WriteHeader(http.StatusUnauthorized)
		return
	case Unauthorized:
		w.WriteHeader(http.StatusForbidden)
		return
	case Internal, Database:
		// Mask raw database/internal messages to avoid leaking sensitive data
		res = ErrResponse{
			Error: ServiceError{
				Kind:    e.Kind.String(),
				Message: "An internal error occurred. Please contact support.",
			},
		}
	default:
		res = ErrResponse{
			Error: ServiceError{
				Kind:    e.Kind.String(),
				Code:    string(e.Code),
				Param:   string(e.Param),
				Message: e.Error(),
			},
		}
	}

	sendJSON(w, statusCode, res)
}

// httpStatusCode maps an error Kind to an HTTP status code.
func httpStatusCode(k Kind) int {
	switch k {
	case Validation, Invalid:
		return http.StatusBadRequest
	case Unauthenticated:
		return http.StatusUnauthorized
	case Unauthorized:
		return http.StatusForbidden
	case Database, Internal:
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}

func sendJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
