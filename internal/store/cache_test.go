package store_test

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/mikael/kubit/internal/store"
)

func TestSettingsCacheInvalidatesAndCopies(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	v, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	v.DiscoverySubnets = append(v.DiscoverySubnets, "10.0.0.0/24")
	v.Alerts.IgnoreNamespaces = []string{"kube-system"}
	v.Alerts.SMTP.Password = "hunter2"
	if err := s.PutSettings(ctx, v); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetSettings(ctx)
	if len(got.DiscoverySubnets) != 1 || got.Alerts.SMTP.Password != "hunter2" {
		t.Fatalf("a write must invalidate the cache: %+v", got)
	}
	got.DiscoverySubnets[0] = "mutated"
	got.Alerts.IgnoreNamespaces[0] = "mutated"
	got.Auth.OIDC.AdminGroups = append(got.Auth.OIDC.AdminGroups, "mutated")
	again, _ := s.GetSettings(ctx)
	if again.DiscoverySubnets[0] != "10.0.0.0/24" || again.Alerts.IgnoreNamespaces[0] != "kube-system" || len(again.Auth.OIDC.AdminGroups) != 0 {
		t.Fatalf("callers must not reach the cached value: %+v", again)
	}
	if again.Auth.OIDC.AdminGroups == nil || again.Alerts.SMTP.To == nil {
		t.Error("empty lists must stay empty, not null")
	}
	again.WatchIntervalSec = 99
	if err := s.PutSettings(ctx, again); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.GetSettings(ctx); v.WatchIntervalSec != 99 {
		t.Errorf("second write not seen: %d", v.WatchIntervalSec)
	}
}

func TestOperationLogAppendsAfterLegacyColumn(t *testing.T) {
	c, _ := store.NewCrypto(bytes.Repeat([]byte{7}, 32))
	dir := t.TempDir()
	s, err := store.Open(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	legacy, _ := s.CreateOperation(ctx, "", "old.op", nil)
	fresh, _ := s.CreateOperation(ctx, "", "new.op", nil)
	s.Close()
	db, err := sql.Open("sqlite", filepath.Join(dir, "kubit.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DROP TABLE operation_log`, `DELETE FROM schema_version WHERE version >= 17`, `UPDATE operations SET log = 'one' || char(10) || 'two' || char(10) WHERE kind = 'old.op'`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	s, err = store.Open(dir, c)
	if err != nil {
		t.Fatalf("migration on an existing database: %v", err)
	}
	defer s.Close()
	for _, line := range []string{"three", "four"} {
		if err := s.AppendOperationLog(ctx, legacy, line); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.AppendOperationLog(ctx, fresh, "a")
	_ = s.AppendOperationLog(ctx, fresh, "b")
	if op, _ := s.GetOperation(ctx, legacy); op.Log != "one\ntwo\nthree\nfour\n" {
		t.Errorf("legacy log: %q", op.Log)
	}
	if op, _ := s.GetOperation(ctx, fresh); op.Log != "a\nb\n" {
		t.Errorf("new log: %q", op.Log)
	}
	if op, _ := s.GetOperationWithoutLog(ctx, fresh); op.Log != "" || op.Kind != "new.op" {
		t.Errorf("without log: %+v", op)
	}
	if err := s.MarkStaleOperations(ctx); err != nil {
		t.Fatal(err)
	}
	op, _ := s.GetOperation(ctx, legacy)
	if op.Status != "failed" || op.Log != "one\ntwo\nthree\nfour\nkubit restarted while this operation was running\n" {
		t.Errorf("stale: %s %q", op.Status, op.Log)
	}
}

func TestTokenLastUsedIsThrottled(t *testing.T) {
	c, _ := store.NewCrypto(bytes.Repeat([]byte{7}, 32))
	dir := t.TempDir()
	s, err := store.Open(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.CreateUser(ctx, "ann", "correct horse battery", store.RoleAdmin, "local"); err != nil {
		t.Fatal(err)
	}
	token, err := s.IssueToken(ctx, "ann", "api", "ci", 0)
	if err != nil {
		t.Fatal(err)
	}
	lastUsed := func() string {
		list, _ := s.ListTokens(ctx, "ann")
		return list[0].LastUsed
	}
	if _, _, err := s.ResolveToken(ctx, token); err != nil {
		t.Fatal(err)
	}
	if lastUsed() == "" {
		t.Fatal("first use must be recorded")
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "kubit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE sessions SET last_used = ''`); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if _, _, err := s.ResolveToken(ctx, token); err != nil {
			t.Fatal(err)
		}
	}
	if got := lastUsed(); got != "" {
		t.Errorf("uses within a minute must not write again: %q", got)
	}
}

func TestLatestOperationAndLastFinishedByKind(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	first, _ := s.CreateOperation(ctx, "c", "platform.plan", nil)
	_ = s.FinishOperation(ctx, first, "done")
	second, _ := s.CreateOperation(ctx, "c", "platform.plan", nil)
	_ = s.FinishOperation(ctx, second, "done")
	failed, _ := s.CreateOperation(ctx, "c", "platform.plan", nil)
	_ = s.FinishOperation(ctx, failed, "failed")
	if got := s.LatestOperation(ctx, "c", "platform.plan", "done"); got != second {
		t.Errorf("latest done plan: %d, want %d", got, second)
	}
	if got := s.LatestOperation(ctx, "other", "platform.plan", "done"); got != 0 {
		t.Errorf("other cluster: %d", got)
	}
	if s.LastFinished(ctx, "c", []string{"node.reboot", "platform.plan"}).IsZero() {
		t.Error("finished plans must count")
	}
	if !s.LastFinished(ctx, "c", nil).IsZero() {
		t.Error("no kinds, no time")
	}
}

func TestOpenWorkloadEvents(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	for _, e := range []store.EventRow{
		{Cluster: "c", Node: "default/web", Severity: "warn", Kind: "workload.degraded", Message: "m"},
		{Cluster: "c", Node: "cp-1", Severity: "warn", Kind: "node.notready", Message: "m"},
		{Cluster: "d", Node: "default/web", Severity: "warn", Kind: "workload.degraded", Message: "m"},
	} {
		if _, err := s.AddEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.OpenWorkloadEvents(ctx, "c", 500)
	if err != nil || len(got) != 1 || got[0].Node != "default/web" {
		t.Fatalf("got %+v %v", got, err)
	}
	_ = s.AckClusterEvents(ctx, "c")
	if got, _ := s.OpenWorkloadEvents(ctx, "c", 500); len(got) != 0 {
		t.Errorf("acked events must drop out: %+v", got)
	}
}
