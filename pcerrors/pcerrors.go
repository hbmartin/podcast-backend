// Package pcerrors emits the Pocket Casts client error envelope. Clients
// decode failed responses (any status) as JSON {"errorMessageId": "..."} and
// map the id onto their APIError enum (ErrorResponse.swift in the iOS app),
// so the id strings below must match the client exactly.
package pcerrors

import (
	"encoding/json"
	"net/http"
)

// Error message ids understood by the client.
const (
	IncorrectPassword     = "login_password_incorrect"
	PermissionDenied      = "login_permission_denied_not_admin"
	AccountLocked         = "login_account_locked"
	BlankEmail            = "login_email_blank"
	BlankPassword         = "login_password_blank"
	EmailNotFound         = "login_email_not_found"
	UnableToCreateAccount = "login_unable_to_create_account"
	PasswordInvalid       = "login_password_invalid"
	EmailInvalid          = "login_email_invalid"
	EmailTaken            = "login_email_taken"
	UserRegisterFailed    = "login_user_register_failed"
	ExpiredToken          = "expired_token"
	AccessDenied          = "access_denied"
	InvalidGrant          = "invalid_grant"
)

type envelope struct {
	ErrorMessageID string `json:"errorMessageId"`
	ErrorMessage   string `json:"errorMessage,omitempty"`
}

// Write replies with the given HTTP status and a client-parseable error body.
func Write(w http.ResponseWriter, status int, messageID string, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, _ := json.Marshal(envelope{ErrorMessageID: messageID, ErrorMessage: message})
	w.Write(body)
}
