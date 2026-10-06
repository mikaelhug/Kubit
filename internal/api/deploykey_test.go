package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/repo"
	"github.com/mikael/kubit/internal/sops"
	"golang.org/x/crypto/ssh"
)

func TestDeployKeyIsWrittenEncryptedAndRefusedWhenStale(t *testing.T) {
	addr, hostKey := sshHost(t)
	s, dir := servedLab(t)
	spec := labSpec + "  platform:\n    flux: {enabled: true, repository: {url: \"ssh://git@" + addr + "/you/apps.git\"}}\n"
	if err := os.WriteFile(filepath.Join(dir, repo.ClusterFile), []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	var before repo.DeployKey
	if rec := call(t, s, "GET", "/api/v1/clusters/lab/flux/key", ""); json.Unmarshal(rec.Body.Bytes(), &before) != nil || before.Key != nil {
		t.Fatalf("before: %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, s, "POST", "/api/v1/clusters/lab/flux/key", `{"hash":"0000000000000000"}`); rec.Code != http.StatusConflict {
		t.Errorf("stale: %d %s", rec.Code, rec.Body)
	}
	rec := call(t, s, "POST", "/api/v1/clusters/lab/flux/key", `{"hash":"`+before.Hash+`"}`)
	var after repo.DeployKey
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &after) != nil || after.Key == nil {
		t.Fatalf("generate: %d %s", rec.Code, rec.Body)
	}
	if len(after.Hosts) != 1 || after.Hosts[0].Fingerprint != ssh.FingerprintSHA256(hostKey) {
		t.Errorf("host keys: %+v", after.Hosts)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, repo.SecretsFile))
	if strings.Contains(string(raw), "PRIVATE KEY") {
		t.Fatal("the deploy key must be written encrypted")
	}
	ids, err := sops.Identities()
	if err != nil {
		t.Fatal(err)
	}
	r, err := repo.LoadWith(dir, ids)
	if err != nil || !strings.HasPrefix(r.Secrets.DeployKey, "-----BEGIN OPENSSH PRIVATE KEY-----") {
		t.Fatalf("decrypted key: %v", err)
	}
}

func sshHost(t *testing.T) (string, ssh.PublicKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _, _, _ = ssh.NewServerConn(c, cfg)
			}()
		}
	}()
	return ln.Addr().String(), signer.PublicKey()
}
