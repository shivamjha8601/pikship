// Package handlers contains chi HTTP handlers for every API route.
package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	code := http.StatusInternalServerError
	msg := "internal server error"
	switch {
	case errors.Is(err, core.ErrNotFound):
		code, msg = http.StatusNotFound, err.Error()
	case errors.Is(err, core.ErrInvalidArgument):
		code, msg = http.StatusBadRequest, err.Error()
	case errors.Is(err, core.ErrConflict):
		code, msg = http.StatusConflict, err.Error()
	case errors.Is(err, core.ErrPermissionDenied):
		code, msg = http.StatusForbidden, err.Error()
	case errors.Is(err, core.ErrUnavailable):
		code, msg = http.StatusServiceUnavailable, err.Error()
	}
	writeJSON(w, code, map[string]string{"error": msg})
}

func decode(r *http.Request, dst any) error {
	return json.NewDecoder(r.Body).Decode(dst)
}
