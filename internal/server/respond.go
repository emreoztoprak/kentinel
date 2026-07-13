package server

import (
	"encoding/json"
	"net/http"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

type errorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError maps an error to an HTTP status and a consistent JSON envelope.
// Kubernetes API errors keep their semantics (404, 403, 409, ...).
func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "internal_error"

	switch {
	case apierrors.IsNotFound(err):
		status, code = http.StatusNotFound, "not_found"
	case apierrors.IsForbidden(err):
		status, code = http.StatusForbidden, "forbidden"
	case apierrors.IsUnauthorized(err):
		status, code = http.StatusUnauthorized, "unauthorized"
	case apierrors.IsConflict(err):
		status, code = http.StatusConflict, "conflict"
	case apierrors.IsInvalid(err), apierrors.IsBadRequest(err):
		status, code = http.StatusUnprocessableEntity, "invalid"
	case isClientInputError(err):
		status, code = http.StatusBadRequest, "bad_request"
	}

	writeJSON(w, status, errorResponse{Error: code, Message: err.Error()})
}

// isClientInputError detects our own validation errors (unsupported kind,
// invalid YAML, manifest mismatch) which should surface as 400s.
func isClientInputError(err error) bool {
	msg := err.Error()
	for _, marker := range []string{
		"unsupported resource kind",
		"invalid YAML",
		"does not match",
		"is not a valid",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func writeBadRequest(w http.ResponseWriter, message string) {
	writeJSON(w, http.StatusBadRequest, errorResponse{Error: "bad_request", Message: message})
}
