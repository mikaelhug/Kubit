package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "release-sign:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: release-sign keygen | sign <file> | verify <file> <public-key> [signature-file]")
	}
	switch args[0] {
	case "keygen":
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		fmt.Println("private", base64.StdEncoding.EncodeToString(priv.Seed()))
		fmt.Println("public ", base64.StdEncoding.EncodeToString(pub))
		return nil
	case "sign":
		if len(args) != 2 {
			return errors.New("usage: release-sign sign <file>")
		}
		return sign(args[1])
	case "verify":
		if len(args) != 3 && len(args) != 4 {
			return errors.New("usage: release-sign verify <file> <public-key> [signature-file]")
		}
		sig := args[1] + ".sig"
		if len(args) == 4 {
			sig = args[3]
		}
		return verify(args[1], args[2], sig)
	}
	return fmt.Errorf("unknown command %q", args[0])
}

func sign(path string) error {
	seed, err := decode(os.Getenv("RELEASE_KEY"), ed25519.SeedSize)
	if err != nil {
		return fmt.Errorf("RELEASE_KEY: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sig := ed25519.Sign(ed25519.NewKeyFromSeed(seed), data)
	return os.WriteFile(path+".sig", []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0o644)
}

func verify(path, pubKey, sigPath string) error {
	pub, err := decode(pubKey, ed25519.PublicKeySize)
	if err != nil {
		return fmt.Errorf("public key: %w", err)
	}
	raw, err := os.ReadFile(sigPath)
	if err != nil {
		return err
	}
	sig, err := decode(string(raw), ed25519.SignatureSize)
	if err != nil {
		return fmt.Errorf("%s: %w", sigPath, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), data, sig) {
		return fmt.Errorf("%s: signature does not match", path)
	}
	fmt.Println(path, "verified")
	return nil
}

func decode(s string, size int) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, err
	}
	if len(b) != size {
		return nil, fmt.Errorf("%d bytes, want %d", len(b), size)
	}
	return b, nil
}
