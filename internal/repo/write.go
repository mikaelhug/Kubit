package repo

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"

	"github.com/mikaelhug/kubit/internal/fsx"
)

var (
	ErrStale     = errors.New("the file changed on disk; reload it and edit again")
	errNoVersion = errors.New("the version of the file you edited is required")
)

func Fingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

func requireHash(hash string) error {
	if hash == "" {
		return errNoVersion
	}
	return nil
}

func replace(path, hash string, perm os.FileMode, edit func(old []byte) ([]byte, error)) error {
	unlock := fsx.Lock(path)
	defer unlock()
	old, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if hash != "" && hash != Fingerprint(old) {
		return ErrStale
	}
	out, err := edit(old)
	if err != nil || out == nil {
		return err
	}
	return fsx.WriteFile(path, out, perm)
}

func create(path string, perm os.FileMode, data []byte) error {
	unlock := fsx.Lock(path)
	defer unlock()
	if _, err := os.Stat(path); err == nil {
		return os.ErrExist
	}
	return fsx.WriteFile(path, data, perm)
}
