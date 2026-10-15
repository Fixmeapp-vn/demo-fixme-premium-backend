package httpx

import (
	"encoding/json"
	"net/http"
)

// ProblemContentType is the media type defined by RFC 9457.
const ProblemContentType = "application/problem+json"

// Code is a stable, machine-readable error identifier. Modules define their
// own codes using a "<module>.<reason>" naming scheme, e.g. "user.not_found".
type Code string

// Platform-level error codes.
const (
	CodeNotFound         Code = "http.not_found"
	CodeMethodNotAllowed Code = "http.method_not_allowed"
	CodeInternal         Code = "http.internal_error"
)

// Problem is an RFC 9457 Problem Details object extended with Code.
type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
	Code   Code   `json:"code"`
}

// NewProblem builds a Problem with type "about:blank" and the standard
// status text as its title.
func NewProblem(status int, code Code, detail string) Problem {
	return Problem{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
		Code:   code,
	}
}

// WriteProblem writes p as an application/problem+json response.
func WriteProblem(w http.ResponseWriter, p Problem) {
	w.Header().Set("Content-Type", ProblemContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

// NotFound responds with the standard 404 problem.
func NotFound(w http.ResponseWriter, _ *http.Request) {
	WriteProblem(w, NewProblem(http.StatusNotFound, CodeNotFound, "The requested resource was not found."))
}

// InternalError responds with the standard 500 problem. It never includes
// the underlying error, which must be logged by the caller instead.
func InternalError(w http.ResponseWriter, _ *http.Request) {
	WriteProblem(w, NewProblem(http.StatusInternalServerError, CodeInternal, "An unexpected error occurred."))
}

func methodNotAllowed(w http.ResponseWriter, allowed []string) {
	for _, m := range allowed {
		w.Header().Add("Allow", m)
	}
	WriteProblem(w, NewProblem(http.StatusMethodNotAllowed, CodeMethodNotAllowed,
		"The requested method is not supported for this resource."))
}
