package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

const (
	MinPassphraseLen = 8

	saltLen = 16
	keyLen  = 32
	timeCost = 1
	memoryCost = 64 * 1024
	threads = 4
	domainAuthKey = "finorama-passphrase-auth-v1"
)

// DeriveKey derives a 32-byte AES key from a passphrase and salt.
func DeriveKey(passphrase string, salt []byte) []byte {
	return argon2.IDKey([]byte(passphrase), salt, timeCost, memoryCost, threads, keyLen)
}

// GenerateSalt returns random salt bytes.
func GenerateSalt() ([]byte, error) {
	b := make([]byte, saltLen)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return nil, err
	}
	return b, nil
}

// Encrypt encrypts plaintext using AES-256-GCM with key.
// Returns base64 encoded string: salt(16) + nonce(12) + ciphertext.
func Encrypt(key []byte, plaintext []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt decrypts base64 encoded ciphertext (nonce + payload) with key.
func Decrypt(key []byte, encoded string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	return gcm.Open(nil, nonce, ciphertext, nil)
}

// HashPassphrase generates verification hash stored in DB to confirm unlock passphrase.
func HashPassphrase(passphrase string, salt []byte) string {
	derived := argon2.IDKey([]byte(passphrase), salt, timeCost, memoryCost, threads, keyLen)
	mac := hmac.New(sha256.New, []byte(domainAuthKey))
	mac.Write(derived)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// VerifyPassphrase compares passphrase against stored hash using constant time comparison.
func VerifyPassphrase(passphrase string, salt []byte, expectedHash string) bool {
	computed := HashPassphrase(passphrase, salt)
	return subtle.ConstantTimeCompare([]byte(computed), []byte(expectedHash)) == 1
}

// GenerateRandomToken generates secure random string for web session.
func GenerateRandomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", fmt.Errorf("failed to generate random token: %w", err)
	}
	return base64.URLEncoding.EncodeToString(b), nil
}
