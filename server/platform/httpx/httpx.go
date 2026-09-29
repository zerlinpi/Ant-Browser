package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const maxJSONBody = 1 << 20

type contextKey string

const requestIDKey contextKey = "request-id"

type ErrorBody struct {
	Error APIError `json:"error"`
}

type APIError struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
	TraceID string      `json:"traceId"`
}

type Problem struct {
	Status  int
	Code    string
	Message string
	Details interface{}
}

func (p Problem) Error() string { return p.Message }

func DecodeJSON(w http.ResponseWriter, r *http.Request, target interface{}) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return Problem{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Request body is invalid", Details: err.Error()}
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Problem{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Request body must contain one JSON value"}
	}
	return nil
}

func WriteJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	problem := Problem{Status: http.StatusInternalServerError, Code: "internal_error", Message: "The request could not be completed"}
	if errors.As(err, &problem) {
		if problem.Status == 0 {
			problem.Status = http.StatusInternalServerError
		}
	}
	WriteJSON(w, problem.Status, ErrorBody{Error: APIError{
		Code: problem.Code, Message: problem.Message, Details: problem.Details, TraceID: RequestID(r.Context()),
	}})
}

func RequestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if len(requestID) < 8 || len(requestID) > 128 {
			requestID = uuid.NewString()
		}
		w.Header().Set("X-Request-ID", requestID)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, requestID)))
	})
}

func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// CORS permits authenticated browser clients only from an explicit set of
// origins. Wildcards are intentionally unsupported because desktop and web
// clients send bearer credentials and idempotency keys.
func CORS(allowedOrigins []string, next http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		if origin = strings.TrimSpace(origin); origin != "" {
			allowed[strings.ToLower(origin)] = struct{}{}
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		if _, ok := allowed[strings.ToLower(origin)]; !ok {
			WriteError(w, r, Problem{Status: http.StatusForbidden, Code: "origin_forbidden", Message: "Browser origin is not allowed"})
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Add("Vary", "Origin")
		// Retry-After is exposed so browser clients can honor 429 responses.
		w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID, Retry-After")
		if r.Method != http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		requestedMethod := strings.ToUpper(strings.TrimSpace(r.Header.Get("Access-Control-Request-Method")))
		allowedMethod := false
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			if requestedMethod == method {
				allowedMethod = true
				break
			}
		}
		if !allowedMethod {
			WriteError(w, r, Problem{Status: http.StatusForbidden, Code: "cors_method_forbidden", Message: "Browser request method is not allowed"})
			return
		}
		allowedHeaders := map[string]struct{}{
			"authorization": {}, "content-type": {}, "idempotency-key": {}, "x-device-id": {}, "x-request-id": {},
		}
		for _, header := range strings.Split(r.Header.Get("Access-Control-Request-Headers"), ",") {
			header = strings.ToLower(strings.TrimSpace(header))
			if header == "" {
				continue
			}
			if _, ok := allowedHeaders[header]; !ok {
				WriteError(w, r, Problem{Status: http.StatusForbidden, Code: "cors_header_forbidden", Message: "Browser request header is not allowed"})
				return
			}
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, X-Device-ID, X-Request-ID")
		w.Header().Set("Access-Control-Max-Age", "600")
		w.WriteHeader(http.StatusNoContent)
	})
}

func AccessLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		logger.InfoContext(r.Context(), "http_request",
			"method", r.Method,
			"path", r.URL.Path,
			"request_id", RequestID(r.Context()),
			"duration_ms", time.Since(started).Milliseconds(),
		)
	})
}

func Recover(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.ErrorContext(r.Context(), "http_panic", "request_id", RequestID(r.Context()), "error", recovered)
				WriteError(w, r, Problem{Status: http.StatusInternalServerError, Code: "internal_error", Message: "The request could not be completed"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}
