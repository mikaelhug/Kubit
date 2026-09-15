package store

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestMasterKeyFileFallback(t *testing.T) {
	t.Setenv(EnvMasterKey, "")
	dir := t.TempDir()
	want := bytes.Repeat([]byte{7}, 32)
	if _, err := mintKeyFile(filepath.Join(dir, MasterKeyFile), want); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, MasterKeyFile))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode %v err %v", st.Mode(), err)
	}
	got, err := LoadMasterKeyIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) || MasterKeySource() != "file" {
		t.Fatalf("got %x source %s", got, MasterKeySource())
	}
	// The environment wins over the file.
	t.Setenv(EnvMasterKey, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)))
	got, err = LoadMasterKeyIn(dir)
	if err != nil || got[0] != 9 || MasterKeySource() != "env" {
		t.Fatalf("env override: %x %s %v", got, MasterKeySource(), err)
	}
}
