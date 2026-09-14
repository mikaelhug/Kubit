package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"

	"github.com/zalando/go-keyring"
)

const (
	keyringService = "kubit"
	keyringUser    = "master-key"
	// EnvMasterKey overrides the keyring with a base64 32-byte key (CI, Linux without a
	// secret service, tests).
	EnvMasterKey = "KUBIT_MASTER_KEY"
)

// Crypto seals secret columns with AES-256-GCM under one master key.
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

// LoadCrypto returns the master key from $KUBIT_MASTER_KEY or the OS keyring, minting
// and storing a new one in the keyring on first use.
func LoadCrypto() (*Crypto, error) {
	key, err := LoadMasterKey()
	if err != nil {
		return nil, err
	}
	return NewCrypto(key)
}

// LoadMasterKey returns the raw 32-byte master key (see LoadCrypto).
func LoadMasterKey() ([]byte, error) {
	if v := os.Getenv(EnvMasterKey); v != "" {
		key, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", EnvMasterKey, err)
		}
		return key, nil
	}
	v, err := keyring.Get(keyringService, keyringUser)
	switch {
	case errors.Is(err, keyring.ErrNotFound):
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := keyring.Set(keyringService, keyringUser, base64.StdEncoding.EncodeToString(key)); err != nil {
			return nil, fmt.Errorf("store master key in keyring: %w", err)
		}
		return key, nil
	case err != nil:
		return nil, fmt.Errorf("read master key from keyring: %w", err)
	}
	key, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return nil, fmt.Errorf("keyring master key: %w", err)
	}
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
