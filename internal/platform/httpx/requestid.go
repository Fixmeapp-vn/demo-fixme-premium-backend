package httpx

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/platform/logging"
)

// RequestIDHeader carries the request ID in requests and responses.
const RequestIDHeader = "X-Request-ID"

// MaxRequestIDLength is the longest incoming request ID that is accepted.
const MaxRequestIDLength = 128

// ValidRequestID reports whether id is an acceptable incoming request ID:
// 1 to 128 characters from [A-Za-z0-9], '-', '_', '.' and ':'.
func ValidRequestID(id string) bool {
	if id == "" || len(id) > MaxRequestIDLength {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-', c == '_', c == '.', c == ':':
		default:
			return false
		}
	}
	return true
}

// RequestID accepts a valid incoming X-Request-ID or generates a new one,
// stores it in the request context and echoes it in the response.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if !ValidRequestID(id) {
			id = newRequestID()
		}
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(logging.WithRequestID(r.Context(), id)))
	})
}

// RequestIDFrom returns the request ID established by the RequestID middleware.
func RequestIDFrom(r *http.Request) string {
	id, _ := logging.RequestIDFromContext(r.Context())
	return id
}

func newRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
