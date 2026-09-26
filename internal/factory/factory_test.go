package factory

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSchematicIDsAreMemoizedPerExtensionSetAndBase(t *testing.T) {
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		id := "none"
		if strings.Contains(string(body), "gvisor") {
			id = "with-gvisor"
		}
		_, _ = io.WriteString(w, `{"id":"`+id+`"}`)
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client()}
	c.SetBaseURL(srv.URL)
	ctx := context.Background()
	a, err := c.CreateSchematic(ctx, []string{"siderolabs/gvisor", "siderolabs/iscsi-tools"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := c.CreateSchematic(ctx, []string{"siderolabs/iscsi-tools", "siderolabs/gvisor"})
	if a != "with-gvisor" || b != a || posts.Load() != 1 {
		t.Errorf("same set in any order is one request: %s %s, %d posts", a, b, posts.Load())
	}
	if none, _ := c.CreateSchematic(ctx, nil); none != "none" || posts.Load() != 2 {
		t.Errorf("a different set asks again: %s, %d posts", none, posts.Load())
	}
	c.SetBaseURL(srv.URL + "/")
	_, _ = c.CreateSchematic(ctx, nil)
	if posts.Load() != 3 {
		t.Errorf("another factory asks again: %d posts", posts.Load())
	}
}
