package agent

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// loadOrCreateSettingsKey returns the AES-256 key used to encrypt settings
// at rest, generating and persisting one on first use. The key lives next
// to the SQLite file it protects, on the same volume — no separate
// Kubernetes Secret or mount is needed, and it works identically across
// Docker, Helm, and raw-manifest deployments.
func loadOrCreateSettingsKey(dbPath string) ([]byte, error) {
	keyPath := filepath.Join(filepath.Dir(dbPath), ".settings.key")

	if data, err := os.ReadFile(keyPath); err == nil {
		key, decodeErr := hex.DecodeString(strings.TrimSpace(string(data)))
		if decodeErr != nil || len(key) != 32 {
			return nil, fmt.Errorf("settings key file %s is corrupt", keyPath)
		}
		return key, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading settings key: %w", err)
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generating settings key: %w", err)
	}
	if err := os.WriteFile(keyPath, []byte(hex.EncodeToString(key)), 0o600); err != nil {
		return nil, fmt.Errorf("writing settings key: %w", err)
	}
	return key, nil
}

// encryptSettings seals plaintext with AES-256-GCM, prefixing the nonce.
func encryptSettings(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// decryptSettings reverses encryptSettings.
func decryptSettings(key, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ct := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ct, nil)
}
