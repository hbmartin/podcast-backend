// Package errs provides structured, context-rich errors that build an
// operation stack trace (OpStack) as they propagate up the call stack,
// while ensuring database details never leak to clients.
package errs

import (
	"errors"
)

// Op describes an operation, usually as the package and method,
// such as "db/CachingQuerier.GetPersonById".
type Op string

// Kind defines the kind of error this is, mostly for use by systems
// that must act differently depending on the error (e.g. HTTP status mapping).
type Kind uint8

// Code is a human-readable, short representation of the error,
// e.g. "invalid_email_format".
type Code string

// Parameter represents the request parameter related to the error.
type Parameter string

// Realm is a description of a protected area, used in the WWW-Authenticate
// header of 401 Unauthorized responses.
type Realm string

// Error is the fundamental error struct. It wraps an underlying standard
// Go error, adding structural context.
type Error struct {
	// Op is the operation being performed, usually the name of the
	// method being invoked.
	Op Op
	// Kind is the class of error, such as a validation error,
	// or "Other" if its class is unknown or irrelevant.
	Kind Kind
	// Param represents the parameter related to the error.
	Param Parameter
	// Code is a human-readable, short representation of the error.
	Code Code
	// Realm is the section of the api the request is trying to access
	// (for 401 Unauthorized errors).
	Realm Realm
	// Err is the underlying error that triggered this one, if any.
	Err error
}

// Unwrap returns the wrapped error, enabling errors.Is/errors.As traversal.
func (e *Error) Unwrap() error {
	return e.Err
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return "unknown error"
}

// Kinds of errors.
const (
	Other           Kind = iota // Unclassified error
	Invalid                     // Invalid operation for this type of item
	Database                    // Error from database
	Internal                    // Internal error or inconsistency
	Validation                  // Input validation error
	Unauthenticated             // Unauthenticated request
	Unauthorized                // Unauthorized request
)

func (k Kind) String() string {
	switch k {
	case Invalid:
		return "invalid operation"
	case Database:
		return "database error"
	case Internal:
		return "internal error"
	case Validation:
		return "input validation error"
	case Unauthenticated:
		return "unauthenticated request"
	case Unauthorized:
		return "unauthorized request"
	default:
		return "error"
	}
}

// E builds an error value from its arguments. There must be at least one
// argument or E panics. The type of each argument determines its meaning:
//
//	Op        The operation being performed.
//	Kind      The class of error.
//	Parameter The request parameter related to the error.
//	Code      A short machine-readable error code.
//	Realm     The realm for WWW-Authenticate headers on 401 errors.
//	string    Treated as an error message and assigned to the Err field.
//	error     The underlying error that triggered this one.
//
// If Kind is not specified or Other, E sets it to the Kind of the
// underlying error, if any.
func E(args ...interface{}) error {
	if len(args) == 0 {
		panic("call to errs.E with no arguments")
	}
	e := &Error{}
	for _, arg := range args {
		switch arg := arg.(type) {
		case Op:
			e.Op = arg
		case Kind:
			e.Kind = arg
		case Parameter:
			e.Param = arg
		case Code:
			e.Code = arg
		case Realm:
			e.Realm = arg
		case string:
			e.Err = errors.New(arg)
		case *Error:
			// Make a copy so error chains are immutable.
			copyErr := *arg
			e.Err = &copyErr
		case error:
			e.Err = arg
		}
	}

	// If Kind was not set, pull it up from the wrapped error, if any.
	if e.Kind == Other && e.Err != nil {
		var subErr *Error
		if errors.As(e.Err, &subErr) {
			e.Kind = subErr.Kind
		}
	}
	return e
}

// OpStack extracts the stack of operations from an error chain, ordered
// chronologically from the root (deepest) operation to the surface one.
func OpStack(err error) []string {
	var ops []string
	e := err
	for e != nil {
		var errsError *Error
		if errors.As(e, &errsError) {
			if errsError.Op != "" {
				ops = append(ops, string(errsError.Op))
			}
			e = errsError.Err
		} else {
			break
		}
	}
	// Reverse the stack so it reads chronologically (root first).
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}

// KindIs reports whether any error in err's chain is an *Error of Kind k.
func KindIs(err error, k Kind) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind == k
	}
	return false
}
