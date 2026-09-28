// Package vault seals secrets with AES-256-GCM under a master key that lives
// outside the database — a sops file on the host, mounted into the container.
// A database dump (and the nightly backups made of it) holds only ciphertext.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

var ErrUnavailable = errors.New("vault key not configured")

// Box seals and opens. A nil *Box means "no key": every call fails with
// ErrUnavailable, so callers can hold one unconditionally.
type Box struct {
	aead cipher.AEAD
}

// Load finds the vault key: VAULT_KEY_FILE, else VAULT_KEY (base64, 32
// bytes), else <dataDir>/vault.key — created on first start when dataDir is
// set, so a fresh install has a working vault without extra steps. Back that
// file up: losing it loses every secret in the vault. No key and no dataDir
// returns (nil, nil): the vault is simply off.
func Load(dataDir string) (*Box, error) {
	raw := os.Getenv("VAULT_KEY")
	if path := os.Getenv("VAULT_KEY_FILE"); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("VAULT_KEY_FILE: %w", err)
		}
		raw = string(b)
	}
	if strings.TrimSpace(raw) == "" && dataDir != "" {
		b, err := loadOrCreateKeyFile(filepath.Join(dataDir, "vault.key"))
		if err != nil {
			return nil, err
		}
		raw = string(b)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(key) != 32 {
		return nil, errors.New("vault key must be 32 bytes, base64-encoded (openssl rand -base64 32)")
	}
	return New(key)
}

func loadOrCreateKeyFile(path string) ([]byte, error) {
	if b, err := os.ReadFile(path); err == nil {
		return b, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("vault key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("vault key dir: %w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	enc := []byte(base64.StdEncoding.EncodeToString(key) + "\n")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("vault key: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(enc); err != nil {
		return nil, err
	}
	slog.Warn("created a new vault key — back it up; without it the vault cannot be decrypted", "path", path)
	return enc, nil
}

func New(key []byte) (*Box, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

func (b *Box) Available() bool { return b != nil }

// Seal returns nonce || ciphertext. aad binds the blob to its owner (the
// vault row's random id), so a sealed value copied onto another row does not
// open there.
func (b *Box) Seal(plaintext, aad []byte) ([]byte, error) {
	if b == nil {
		return nil, ErrUnavailable
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, plaintext, aad), nil
}

func (b *Box) Open(sealed, aad []byte) ([]byte, error) {
	if b == nil {
		return nil, ErrUnavailable
	}
	n := b.aead.NonceSize()
	if len(sealed) < n+b.aead.Overhead() {
		return nil, errors.New("sealed value is truncated")
	}
	return b.aead.Open(nil, sealed[:n], sealed[n:], aad)
}
