package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/hbmartin/podcast-backend/artwork"
	"github.com/hbmartin/podcast-backend/attest"
	"github.com/hbmartin/podcast-backend/auth"
	"github.com/hbmartin/podcast-backend/config"
	"github.com/hbmartin/podcast-backend/crawler"
	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/errs"
	"github.com/hbmartin/podcast-backend/itunes"
	"github.com/hbmartin/podcast-backend/middlewares"
	"github.com/hbmartin/podcast-backend/models"
	"github.com/hbmartin/podcast-backend/tasks"
	"log/slog"
	"net/http"
	"reflect"

	"github.com/go-playground/validator/v10"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// SocialPushFunc delivers one social push event (Slice 8): the wiring in
// main.go bridges it to the task queue or a direct notifier goroutine.
type SocialPushFunc func(targetUserID int64, pushType int, actorHandle, actorDisplayName string, data map[string]string)

type Handlers struct {
	Queries db.Store
	Queue   *tasks.QueueClient
	Config  *config.AuthConfiguration
	Crawler *crawler.Crawler
	Search  itunes.Searcher
	Images  artwork.ImageFetcher
	// PublicBaseURL, when set, overrides request-derived base URLs in
	// generated links (see baseURL).
	PublicBaseURL string
	// QueuePing, when set, is consulted by /health to report the task
	// queue's Redis as a dependency.
	QueuePing func(ctx context.Context) error
	// AttestVerifier verifies App Attest material; nil when App Attest is not
	// configured (endpoints then behave as ModeOff regardless of route mode).
	// SocialPush is a late-bound holder: routes capture Handlers by value
	// before the notifier exists, so the pointer is set at construction and
	// the function assigned once push is wired (nil-safe no-op otherwise).
	SocialPush     *SocialPushFunc
	AttestVerifier *attest.Verifier
}

func New(store db.Store) Handlers {
	return Handlers{Queries: store}
}

// NewWithQueue builds Handlers that can also enqueue background tasks and
// mint tokens.
func NewWithQueue(store db.Store, queue *tasks.QueueClient, authConfig *config.AuthConfiguration) Handlers {
	return Handlers{Queries: store, Queue: queue, Config: authConfig}
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

// maxJSONBody caps JSON request bodies (matches the protobuf cap in proto.go).
const maxJSONBody = 4 << 20

func bindJSON(r *http.Request, result any) error {
	err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxJSONBody)).Decode(result)

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

// notifySocial fires the social push seam if it has been wired (Slice 8).
func (h Handlers) notifySocial(targetUserID int64, pushType int, actorHandle, actorDisplayName string, data map[string]string) {
	if h.SocialPush == nil || *h.SocialPush == nil {
		return
	}
	(*h.SocialPush)(targetUserID, pushType, actorHandle, actorDisplayName, data)
}
