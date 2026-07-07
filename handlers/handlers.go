package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"goapi-template/auth"
	"goapi-template/config"
	"goapi-template/db"
	"goapi-template/errs"
	"goapi-template/middlewares"
	"goapi-template/models"
	"goapi-template/tasks"
	"log/slog"
	"net/http"
	"reflect"

	"github.com/go-playground/validator/v10"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Handlers struct {
	Queries db.Querier
	Queue   *tasks.QueueClient
	Config  *config.AuthConfiguration
}

func New(querier db.Querier) Handlers {
	return Handlers{Queries: querier}
}

// NewWithQueue builds Handlers that can also enqueue background tasks and
// mint tokens.
func NewWithQueue(querier db.Querier, queue *tasks.QueueClient, authConfig *config.AuthConfiguration) Handlers {
	return Handlers{Queries: querier, Queue: queue, Config: authConfig}
}

// writeError translates any error into an HTTP response. Well-known
// database errors keep their legacy semantics (404 for missing rows, 409 for
// duplicates) even when wrapped in an *errs.Error; remaining structured
// errors are delegated to errs.HTTPErrorResponse, which logs the operation
// stack and masks internal details from the client.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		slog.Debug("Resource not found", "traceId", r.Context().Value(middlewares.ContextKey("traceId")))
		writeJSON(w, http.StatusNotFound, nil)
		return
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		slog.Warn("Duplicate record rejected", "constraint", pgErr.ConstraintName, "traceId", r.Context().Value(middlewares.ContextKey("traceId")))
		writeJSON(w, http.StatusConflict, &models.ErrorResult{Errors: []string{"Record duplication detected"}})
		return
	}

	var e *errs.Error
	if errors.As(err, &e) {
		errs.HTTPErrorResponse(w, e)
		return
	}

	status, body := errorToHttpResult(err, r.Context())
	writeJSON(w, status, body)
}

func errorToHttpResult(err error, ctx context.Context) (int, *models.ErrorResult) {
	slog.Error("Error handled",
		"error", err,
		"traceId", ctx.Value(middlewares.ContextKey("traceId")),
	)

	if vErrs, ok := err.(validator.ValidationErrors); ok {
		out := translateErrors(vErrs)
		return http.StatusBadRequest, &models.ErrorResult{Errors: out}
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return http.StatusNotFound, nil
	}

	var dbError *pgconn.PgError
	if errors.As(err, &dbError) {
		if dbError.Code == "23505" {
			return http.StatusConflict, &models.ErrorResult{Errors: []string{"Record duplication detected"}}
		}
	}

	return http.StatusInternalServerError, &models.ErrorResult{Errors: []string{"Unknown error"}}
}

func getUser(ctx context.Context) *auth.User {
	if ctx == nil {
		return nil
	}

	if user := ctx.Value(auth.UserKey); user != nil {
		return user.(*auth.User)
	}

	return nil
}

func getUserEmail(ctx context.Context) string {
	if user := getUser(ctx); user != nil {
		return user.Email
	}

	return ""
}

//lint:ignore U1000 used by the JSON refresh/cache host handlers in upcoming milestones
func bindJSON(r *http.Request, result any) error {
	err := json.NewDecoder(r.Body).Decode(result)

	if err != nil {
		return err
	}

	validate := validator.New()
	validate.SetTagName("binding")
	value := reflect.ValueOf(result)
	switch value.Kind() {
	case reflect.Ptr:
		return validate.Struct(value.Elem().Interface())
	case reflect.Struct:
		return validate.Struct(result)
	case reflect.Slice, reflect.Array:
		count := value.Len()
		validateRet := make(models.ValidationErrors, 0)
		for i := 0; i < count; i++ {
			if err := validate.Struct(value.Index(i).Interface()); err != nil {
				validateRet = append(validateRet, err)
			}
		}
		if len(validateRet) == 0 {
			return nil
		}
		return validateRet
	default:
		return nil
	}
}

func writeJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	result, _ := json.Marshal(data)
	w.Write(result)
}

func translateErrors(err validator.ValidationErrors) []string {
	out := make([]string, len(err))
	for i, fe := range err {
		out[i] = getValidationErrorMsg(fe)
	}
	return out
}

func getValidationErrorMsg(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return fmt.Sprintf("%s is required", fe.Field())
	case "lte":
		return fmt.Sprintf("%s should be less than or equal to %s", fe.Field(), fe.Param())
	case "lt":
		return fmt.Sprintf("%s should be less than %s", fe.Field(), fe.Param())
	case "gte":
		return fmt.Sprintf("%s should be greater than or equal to %s", fe.Field(), fe.Param())
	case "gt":
		return fmt.Sprintf("%s should be greater than %s", fe.Field(), fe.Param())
	case "min":
		return fmt.Sprintf("%s should have minimum length of %s", fe.Field(), fe.Param())
	case "max":
		return fmt.Sprintf("%s should have maximum length of %s", fe.Field(), fe.Param())
	case "alpha":
		return fmt.Sprintf("%s should contain alpha characters only", fe.Field())
	}
	return "Unknown error"
}
