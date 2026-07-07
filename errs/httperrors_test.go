package errs

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func decodeErrResponse(t *testing.T, rr *httptest.ResponseRecorder) ErrResponse {
	t.Helper()
	var res ErrResponse
	assert.Nil(t, json.Unmarshal(rr.Body.Bytes(), &res))
	return res
}

func TestHTTPErrorResponseNilError(t *testing.T) {
	rr := httptest.NewRecorder()
	HTTPErrorResponse(rr, nil)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestHTTPErrorResponseUnclassifiedError(t *testing.T) {
	rr := httptest.NewRecorder()
	HTTPErrorResponse(rr, fmt.Errorf("something broke"))

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	res := decodeErrResponse(t, rr)
	assert.Equal(t, "unanticipated_error", res.Error.Kind)
	assert.NotContains(t, res.Error.Message, "something broke")
}

func TestHTTPErrorResponseValidationError(t *testing.T) {
	rr := httptest.NewRecorder()
	err := E(Op("db/GetPersonById"), Validation, Parameter("id"), Code("invalid_id"), "id must be greater than zero")
	HTTPErrorResponse(rr, err)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	res := decodeErrResponse(t, rr)
	assert.Equal(t, "input validation error", res.Error.Kind)
	assert.Equal(t, "invalid_id", res.Error.Code)
	assert.Equal(t, "id", res.Error.Param)
	assert.Equal(t, "id must be greater than zero", res.Error.Message)
}

func TestHTTPErrorResponseMasksDatabaseError(t *testing.T) {
	rr := httptest.NewRecorder()
	err := E(Op("db/GetPersonById"), Database, "pq: password authentication failed for user postgres")
	HTTPErrorResponse(rr, err)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	res := decodeErrResponse(t, rr)
	assert.Equal(t, "database error", res.Error.Kind)
	assert.NotContains(t, res.Error.Message, "postgres")
}

func TestHTTPErrorResponseMasksOtherError(t *testing.T) {
	rr := httptest.NewRecorder()
	err := E(Op("service/DoThing"), "secret token leaked")
	HTTPErrorResponse(rr, err)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	res := decodeErrResponse(t, rr)
	assert.Equal(t, "error", res.Error.Kind)
	assert.NotContains(t, res.Error.Message, "secret token")
	assert.Empty(t, res.Error.Code)
	assert.Empty(t, res.Error.Param)
}

func TestHTTPErrorResponseUnauthenticated(t *testing.T) {
	rr := httptest.NewRecorder()
	err := E(Op("auth/VerifyToken"), Unauthenticated, Realm("api"), "invalid token")
	HTTPErrorResponse(rr, err)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.Equal(t, `Bearer realm="api"`, rr.Header().Get("WWW-Authenticate"))
	assert.Empty(t, rr.Body.String())
}

func TestHTTPErrorResponseUnauthorized(t *testing.T) {
	rr := httptest.NewRecorder()
	err := E(Op("auth/Authorize"), Unauthorized, "missing scope")
	HTTPErrorResponse(rr, err)

	assert.Equal(t, http.StatusForbidden, rr.Code)
	assert.Empty(t, rr.Body.String())
}
