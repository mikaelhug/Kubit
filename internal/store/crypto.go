package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zalando/go-keyring"
)

const (
	keyringService = "kubit"
	keyringUser    = "master-key"
	EnvMasterKey   = "KUBIT_MASTER_KEY"
)

type Crypto struct {
	aead cipher.AEAD
}

func NewCrypto(key []byte) (*Crypto, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("master key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Crypto{aead: aead}, nil
}

const MasterKeyFile = "master.key"

var keySource = "unset"

func MasterKeySource() string { return keySource }

func LoadCrypto() (*Crypto, error) {
	key, err := LoadMasterKey()
	if err != nil {
		return nil, err
	}
	return NewCrypto(key)
}

func HomeDir() (string, error) {
	if h := os.Getenv("KUBIT_HOME"); h != "" {
		return h, nil
	}
	u, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(u, ".kubit"), nil
}

func LoadMasterKey() ([]byte, error) {
	dir, _ := HomeDir()
	return LoadMasterKeyIn(dir)
}

func LoadMasterKeyIn(dir string) ([]byte, error) {
	if v := os.Getenv(EnvMasterKey); v != "" {
		key, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", EnvMasterKey, err)
		}
		keySource = "env"
		return key, nil
	}
	path := filepath.Join(dir, MasterKeyFile)
	if b, err := os.ReadFile(path); err == nil {
		key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		keySource = "file"
		return key, nil
	}
	v, err := keyring.Get(keyringService, keyringUser)
	switch {
	case err == nil:
		key, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return nil, fmt.Errorf("keyring master key: %w", err)
		}
		keySource = "keyring"
		return key, nil
	case errors.Is(err, keyring.ErrNotFound):
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := keyring.Set(keyringService, keyringUser, base64.StdEncoding.EncodeToString(key)); err == nil {
			keySource = "keyring"
			return key, nil
		}
		return mintKeyFile(path, key)
	default:
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		return mintKeyFile(path, key)
	}
}

func mintKeyFile(path string, key []byte) ([]byte, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key)+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("write master key file: %w", err)
	}
	keySource = "file"
	return key, nil
}

func (c *Crypto) Seal(plain []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, plain, nil), nil
}

func (c *Crypto) Open(sealed []byte) ([]byte, error) {
	n := c.aead.NonceSize()
	if len(sealed) < n {
		return nil, errors.New("sealed value too short")
	}
	return c.aead.Open(nil, sealed[:n], sealed[n:], nil)
}

func (s *Store) SealFile(plain []byte) ([]byte, error)  { return s.crypto.Seal(plain) }
func (s *Store) OpenFile(sealed []byte) ([]byte, error) { return s.crypto.Open(sealed) }
