package errs

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEBuildsError(t *testing.T) {
	err := E(Op("db/GetPersonById"), Validation, Parameter("id"), Code("invalid_id"), "id must be greater than zero")

	var e *Error
	assert.True(t, errors.As(err, &e))
	assert.Equal(t, Op("db/GetPersonById"), e.Op)
	assert.Equal(t, Validation, e.Kind)
	assert.Equal(t, Parameter("id"), e.Param)
	assert.Equal(t, Code("invalid_id"), e.Code)
	assert.Equal(t, "id must be greater than zero", err.Error())
}

func TestEWrapsUnderlyingError(t *testing.T) {
	inner := fmt.Errorf("connection refused")
	err := E(Op("db/GetPersonById"), Database, inner)

	assert.True(t, errors.Is(err, inner))
	assert.Equal(t, "connection refused", err.Error())
}

func TestEPanicsWithoutArguments(t *testing.T) {
	assert.Panics(t, func() { _ = E() })
}

func TestEInheritsKindFromWrappedError(t *testing.T) {
	inner := E(Op("db/GetPersonById"), Database, "connection refused")
	outer := E(Op("handlers/GetPerson"), inner)

	var e *Error
	assert.True(t, errors.As(outer, &e))
	assert.Equal(t, Database, e.Kind)
}

func TestErrorWithoutWrappedError(t *testing.T) {
	err := &Error{Op: "op"}

	assert.Equal(t, "unknown error", err.Error())
}

func TestOpStackReadsChronologically(t *testing.T) {
	root := E(Op("db/GetPersonById"), Database, "connection refused")
	mid := E(Op("service/FetchPerson"), root)
	top := E(Op("handlers/GetPerson"), mid)

	assert.Equal(t, []string{"db/GetPersonById", "service/FetchPerson", "handlers/GetPerson"}, OpStack(top))
}

func TestOpStackWithNonErrsError(t *testing.T) {
	assert.Empty(t, OpStack(fmt.Errorf("plain error")))
}

func TestKindString(t *testing.T) {
	assert.Equal(t, "database error", Database.String())
	assert.Equal(t, "internal error", Internal.String())
	assert.Equal(t, "input validation error", Validation.String())
	assert.Equal(t, "unauthenticated request", Unauthenticated.String())
	assert.Equal(t, "unauthorized request", Unauthorized.String())
	assert.Equal(t, "error", Other.String())
}

func TestKindIs(t *testing.T) {
	err := E(Op("op"), Validation, "bad input")

	assert.True(t, KindIs(err, Validation))
	assert.False(t, KindIs(err, Database))
	assert.False(t, KindIs(fmt.Errorf("plain"), Validation))
}

func TestKindIsWalksNestedErrsChain(t *testing.T) {
	root := E(Op("db/GetPersonById"), Database, "connection refused")
	mid := fmt.Errorf("service failed: %w", root)
	top := E(Op("handlers/GetPerson"), Validation, mid)

	assert.True(t, KindIs(top, Validation))
	assert.True(t, KindIs(top, Database))
	assert.False(t, KindIs(top, Internal))
}
