package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type statusError struct {
	Status int
	Msg    string
}

func (e *statusError) Error() string { return e.Msg }

func writeErr(w http.ResponseWriter, err error) {
	var se *statusError
	status := http.StatusInternalServerError
	msg := err.Error()
	switch {
	case errors.As(err, &se):
		status, msg = se.Status, se.Msg
	case errors.Is(err, store.ErrNotFound):
		status = http.StatusNotFound
	case grpcstatus.Code(err) != codes.Unknown && grpcstatus.Code(err) != codes.OK:
		status, msg = talos.HTTPStatus(err), talos.ShortGRPC(err).Error()
	case strings.Contains(msg, "already exists"), strings.Contains(msg, "must"), strings.Contains(msg, "required"):
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]string{"error": msg})
}

func spaHandler(root http.FileSystem) http.Handler {
	files := http.FileServer(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		if f, err := root.Open(r.URL.Path); err == nil && !strings.HasSuffix(r.URL.Path, "/") {
			f.Close()
			files.ServeHTTP(w, r)
			return
		}
		r.URL.Path = "/"
		files.ServeHTTP(w, r)
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	return decodeBody(w, r, v, false)
}

func decodeOptionalJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	return decodeBody(w, r, v, true)
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any, optional bool) bool {
	err := json.NewDecoder(r.Body).Decode(v)
	if err == nil || (optional && errors.Is(err, io.EOF)) {
		return true
	}
	writeErr(w, &statusError{http.StatusBadRequest, "body: " + err.Error()})
	return false
}

func unprocessable(err error) error {
	return &statusError{http.StatusUnprocessableEntity, err.Error()}
}
