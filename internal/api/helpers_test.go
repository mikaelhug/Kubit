package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/store"
)

func TestErrorStatusesAreExplicit(t *testing.T) {
	for _, c := range []struct {
		err    error
		status int
		body   string
	}{
		{&statusError{Status: http.StatusConflict, Msg: "pxe down", Code: "pxe-down", Command: "kubit pxe"}, http.StatusConflict, `{"error":"pxe down","code":"pxe-down","command":"kubit pxe"}`},
		{conflict("busy"), http.StatusConflict, `{"error":"busy"}`},
		{badRequest("name is required"), http.StatusBadRequest, `{"error":"name is required"}`},
		{errors.New("the disk must not be full"), http.StatusInternalServerError, `{"error":"the disk must not be full"}`},
		{fmt.Errorf("cluster x: %w", store.ErrNotFound), http.StatusNotFound, `{"error":"cluster x: not found"}`},
	} {
		rec := httptest.NewRecorder()
		writeErr(rec, c.err)
		if rec.Code != c.status || strings.TrimSpace(rec.Body.String()) != c.body {
			t.Errorf("%v: %d %s, want %d %s", c.err, rec.Code, rec.Body, c.status, c.body)
		}
	}
}

func TestValidationErrorsAnswerBadRequest(t *testing.T) {
	s, _ := localServer(t)
	for _, c := range []struct {
		method, path, body string
	}{
		{"POST", "/api/v1/discover", `{"targets":["10.0.0.1/31"]}`},
		{"POST", "/api/v1/discover", `{"targets":["10.0.0.0/99"]}`},
		{"GET", "/api/v1/clusters/c/snapshots/latest", ``},
	} {
		if rec := call(t, s, c.method, c.path, c.body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s %s: %d %s", c.method, c.path, c.body, rec.Code, rec.Body)
		}
	}
}
