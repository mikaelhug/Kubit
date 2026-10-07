package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/mikaelhug/kubit/internal/store"
	"github.com/mikaelhug/kubit/internal/talos"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func reply(w http.ResponseWriter, v any, err error) {
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func textStream(w http.ResponseWriter) func([]byte) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	fl, _ := w.(http.Flusher)
	return func(b []byte) {
		w.Write(b)
		if fl != nil {
			fl.Flush()
		}
	}
}

type statusError struct {
	Status int
	Msg    string
}

func (e *statusError) Error() string { return e.Msg }

func badRequest(msg string) error { return &statusError{Status: http.StatusBadRequest, Msg: msg} }

func conflict(msg string) error { return &statusError{Status: http.StatusConflict, Msg: msg} }

func invalid(err error) error { return badRequest(err.Error()) }

type errorBody struct {
	Error string `json:"error"`
}

func writeErr(w http.ResponseWriter, err error) {
	var se *statusError
	status := http.StatusInternalServerError
	body := errorBody{Error: err.Error()}
	switch {
	case errors.As(err, &se):
		status, body = se.Status, errorBody{Error: se.Msg}
	case errors.Is(err, store.ErrNotFound):
		status = http.StatusNotFound
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
