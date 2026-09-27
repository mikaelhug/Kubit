package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
)

func TestDecodeJSONBodies(t *testing.T) {
	type body struct {
		To string `json:"to"`
	}
	for _, c := range []struct {
		name     string
		body     string
		optional bool
		ok       bool
		to       string
	}{
		{"required with body", `{"to":"v1"}`, false, true, "v1"},
		{"required without body", ``, false, false, ""},
		{"optional without body", ``, true, true, ""},
		{"optional with body", `{"to":"v2"}`, true, true, "v2"},
		{"optional with garbage", `{"to":`, true, false, ""},
		{"required with garbage", `nope`, false, false, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(c.body))
			var v body
			decode := decodeJSON
			if c.optional {
				decode = decodeOptionalJSON
			}
			if ok := decode(rec, req, &v); ok != c.ok || v.To != c.to {
				t.Fatalf("ok=%v to=%q, want ok=%v to=%q", ok, v.To, c.ok, c.to)
			}
			if !c.ok && (rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"error"`)) {
				t.Errorf("a bad body answers 400 JSON: %d %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestStartOpAnswersAcceptedWithTheOperation(t *testing.T) {
	s, st, _ := localServer(t)
	rec := httptest.NewRecorder()
	s.startOp(rec, "", "test.op", map[string]string{"k": "v"}, func(ctx context.Context, sink cluster.Sink) (any, error) {
		sink.Emit(cluster.Info, "work", "", "hello %s", "there")
		return nil, nil
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d %s", rec.Code, rec.Body)
	}
	var out struct {
		OperationID int64 `json:"operationId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.OperationID == 0 {
		t.Fatalf("body %s: %v", rec.Body, err)
	}
	op := waitOp(t, st, out.OperationID)
	if op.Kind != "test.op" || op.Status != "done" || !strings.Contains(op.Log, "hello there") {
		t.Errorf("operation: %+v", op)
	}
}

func TestAcceptedWritesTheError(t *testing.T) {
	rec := httptest.NewRecorder()
	accepted(rec, 0, conflict("busy"))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "busy") {
		t.Errorf("error reply: %d %s", rec.Code, rec.Body)
	}
}

func TestSettingsSecretsAreMaskedAndRestored(t *testing.T) {
	s, st, _ := localServer(t)
	ctx := t.Context()
	v, _ := st.GetSettings(ctx)
	secrets := v.Secrets()
	for i, p := range secrets {
		*p = "secret-" + string(rune('a'+i))
	}
	if err := st.PutSettings(ctx, v); err != nil {
		t.Fatal(err)
	}
	red := redactSettings(v)
	for i, p := range red.Secrets() {
		if *p != store.Masked {
			t.Errorf("secret %d not masked: %q", i, *p)
		}
	}
	if *secrets[0] != "secret-a" {
		t.Error("redacting must not touch the original")
	}
	*red.Secrets()[2] = "changed"
	s.unmaskSettings(ctx, &red)
	for i, p := range red.Secrets() {
		want := "secret-" + string(rune('a'+i))
		if i == 2 {
			want = "changed"
		}
		if *p != want {
			t.Errorf("secret %d = %q, want %q", i, *p, want)
		}
	}
	empty := redactSettings(store.DefaultSettings())
	for i, p := range empty.Secrets() {
		if *p != "" {
			t.Errorf("an unset secret %d stays empty, got %q", i, *p)
		}
	}
}

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
	s, st, _ := localServer(t)
	for _, c := range []struct {
		method, path, body string
	}{
		{"POST", "/api/v1/users", `{"name":"bob","password":"short","role":"viewer"}`},
		{"POST", "/api/v1/users", `{"name":"b","password":"long enough pass","role":"viewer"}`},
		{"POST", "/api/v1/clusters", `{"yaml":"apiVersion: kubit.dev/v1\nkind: Cluster\nmetadata: {name: x}\nspec:\n  nodes: []\n"}`},
		{"POST", "/api/v1/clusters", `{"yaml":"kind: ["}`},
		{"POST", "/api/v1/discover", `{"targets":["10.0.0.1/31"]}`},
		{"POST", "/api/v1/discover", `{"targets":["10.0.0.0/99"]}`},
		{"GET", "/api/v1/clusters/c/snapshots/latest", ``},
	} {
		if rec := call(t, s, c.method, c.path, c.body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s %s: %d %s", c.method, c.path, c.body, rec.Code, rec.Body)
		}
	}
	if _, err := st.CreateUser(t.Context(), "admin", "long enough pass", store.RoleAdmin, "local"); err != nil {
		t.Fatal(err)
	}
	_, err := st.CreateUser(t.Context(), "admin", "long enough pass", store.RoleAdmin, "local")
	rec := httptest.NewRecorder()
	writeErr(rec, err)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "already exists") {
		t.Errorf("duplicate user: %d %s", rec.Code, rec.Body)
	}
}
