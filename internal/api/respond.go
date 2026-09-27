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
	Status  int
	Msg     string
	Code    string
	Command string
}

func (e *statusError) Error() string { return e.Msg }

func badRequest(msg string) error { return &statusError{Status: http.StatusBadRequest, Msg: msg} }

func conflict(msg string) error { return &statusError{Status: http.StatusConflict, Msg: msg} }

func invalid(err error) error { return badRequest(err.Error()) }

type errorBody struct {
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
	Command string `json:"command,omitempty"`
}

func writeErr(w http.ResponseWriter, err error) {
	var se *statusError
	status := http.StatusInternalServerError
	body := errorBody{Error: err.Error()}
	switch {
	case errors.As(err, &se):
		status, body = se.Status, errorBody{Error: se.Msg, Code: se.Code, Command: se.Command}
	case errors.Is(err, store.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, store.ErrInvalid):
		status = http.StatusBadRequest
	case grpcstatus.Code(err) != codes.Unknown && grpcstatus.Code(err) != codes.OK:
		status, body.Error = talos.HTTPStatus(err), talos.ShortGRPC(err).Error()
	}
	writeJSON(w, status, body)
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
	writeErr(w, badRequest("body: "+err.Error()))
	return false
}

func unprocessable(err error) error {
	return &statusError{Status: http.StatusUnprocessableEntity, Msg: err.Error()}
}
