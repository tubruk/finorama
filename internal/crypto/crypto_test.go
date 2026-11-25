package crypto_test

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"io"
	"net/url"
	"strings"
	"testing"

	"github.com/chickenzord/finorama/internal/crypto"
)

func TestEncryptDecrypt(t *testing.T) {
	passphrase := "supersecret"
	salt, err := crypto.GenerateSalt()
	if err != nil {
		t.Fatalf("GenerateSalt error: %v", err)
	}

	key := crypto.DeriveKey(passphrase, salt)
	plain := "my-ksei-password-123"

	enc, err := crypto.Encrypt(key, []byte(plain))
	if err != nil {
		t.Fatalf("Encrypt error: %v", err)
	}

	dec, err := crypto.Decrypt(key, enc)
	if err != nil {
		t.Fatalf("Decrypt error: %v", err)
	}

	if string(dec) != plain {
		t.Fatalf("expected %s, got %s", plain, string(dec))
	}
}

func TestVerifyPassphrase(t *testing.T) {
	passphrase := "finorama-pass"
	salt, err := crypto.GenerateSalt()
	if err != nil {
		t.Fatalf("GenerateSalt error: %v", err)
	}

	hash := crypto.HashPassphrase(passphrase, salt)

	if !crypto.VerifyPassphrase(passphrase, salt, hash) {
		t.Errorf("expected passphrase verification to succeed")
	}

	if crypto.VerifyPassphrase("wrongpass", salt, hash) {
		t.Errorf("expected wrong passphrase verification to fail")
	}
}

func TestEncryptDecryptTable(t *testing.T) {
	validKey32 := []byte("01234567890123456789012345678901")
	altKey32 := []byte("10987654321098765432109876543210")

	// Pre-generate valid encrypted payload for tampering tests
	samplePlain := []byte("sensitive financial data 12345")
	validEncoded, err := crypto.Encrypt(validKey32, samplePlain)
	if err != nil {
		t.Fatalf("failed to encrypt sample plaintext: %v", err)
	}

	rawCiphertext, err := base64.StdEncoding.DecodeString(validEncoded)
	if err != nil {
		t.Fatalf("failed to decode valid base64: %v", err)
	}

	// Corrupted ciphertext: flip a bit in ciphertext / auth tag part (after 12-byte nonce)
	tamperedRaw := make([]byte, len(rawCiphertext))
	copy(tamperedRaw, rawCiphertext)
	tamperedRaw[len(tamperedRaw)-1] ^= 0xFF
	tamperedEncoded := base64.StdEncoding.EncodeToString(tamperedRaw)

	// Nonce-only data (truncated: 11 bytes, shorter than 12-byte GCM nonce)
	truncated11 := base64.StdEncoding.EncodeToString([]byte("12345678901"))

	// Large plaintext (1 MB)
	largePlain := make([]byte, 1024*1024)
	if _, err := io.ReadFull(rand.Reader, largePlain); err != nil {
		t.Fatalf("failed to generate large random plaintext: %v", err)
	}

	tests := []struct {
		name       string
		key        []byte
		plaintext  []byte
		setupEnc   func() (key []byte, encoded string)
		wantEncErr bool
		wantDecErr bool
	}{
		{
			name:      "Empty plaintext",
			key:       validKey32,
			plaintext: []byte(""),
		},
		{
			name:      "Large plaintext (1MB)",
			key:       validKey32,
			plaintext: largePlain,
		},
		{
			name: "Decrypt invalid base64",
			setupEnc: func() ([]byte, string) {
				return validKey32, "!!!not-valid-base64-string!!!"
			},
			wantDecErr: true,
		},
		{
			name: "Decrypt corrupted ciphertext / tampered bytes",
			setupEnc: func() ([]byte, string) {
				return validKey32, tamperedEncoded
			},
			wantDecErr: true,
		},
		{
			name: "Decrypt truncated data shorter than nonce",
			setupEnc: func() ([]byte, string) {
				return validKey32, truncated11
			},
			wantDecErr: true,
		},
		{
			name: "Decrypt with wrong key (auth tag verification failure)",
			setupEnc: func() ([]byte, string) {
				return altKey32, validEncoded
			},
			wantDecErr: true,
		},
		{
			name:       "Encrypt with invalid key size 10 bytes",
			key:        []byte("1234567890"),
			plaintext:  []byte("test"),
			wantEncErr: true,
		},
		{
			name:       "Encrypt with invalid key size 0 bytes",
			key:        []byte(""),
			plaintext:  []byte("test"),
			wantEncErr: true,
		},
		{
			name:       "Encrypt with invalid key size 17 bytes",
			key:        []byte("01234567890123456"),
			plaintext:  []byte("test"),
			wantEncErr: true,
		},
		{
			name:       "Encrypt with invalid key size 33 bytes",
			key:        []byte("012345678901234567890123456789012"),
			plaintext:  []byte("test"),
			wantEncErr: true,
		},
		{
			name: "Decrypt with invalid key size 10 bytes",
			setupEnc: func() ([]byte, string) {
				return []byte("1234567890"), validEncoded
			},
			wantDecErr: true,
		},
		{
			name: "Decrypt with invalid key size 33 bytes",
			setupEnc: func() ([]byte, string) {
				return []byte("012345678901234567890123456789012"), validEncoded
			},
			wantDecErr: true,
		},
		// AES also supports 16 and 24 byte keys natively:
		{
			name:      "Encrypt and Decrypt with 16 byte key",
			key:       []byte("0123456789012345"),
			plaintext: []byte("testing aes-128"),
		},
		{
			name:      "Encrypt and Decrypt with 24 byte key",
			key:       []byte("012345678901234567890123"),
			plaintext: []byte("testing aes-192"),
		},
		{
			name: "Decrypt 32-byte ciphertext with 16-byte key fails",
			setupEnc: func() ([]byte, string) {
				return []byte("0123456789012345"), validEncoded
			},
			wantDecErr: true,
		},
		{
			name: "Decrypt 32-byte ciphertext with 24-byte key fails",
			setupEnc: func() ([]byte, string) {
				return []byte("012345678901234567890123"), validEncoded
			},
			wantDecErr: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if tc.setupEnc != nil {
				key, encoded := tc.setupEnc()
				dec, err := crypto.Decrypt(key, encoded)
				if tc.wantDecErr {
					if err == nil {
						t.Errorf("expected Decrypt to fail, but succeeded with output: %v", dec)
					}
				} else {
					if err != nil {
						t.Errorf("unexpected Decrypt error: %v", err)
					}
				}
				return
			}

			// Normal encrypt -> decrypt cycle
			enc, err := crypto.Encrypt(tc.key, tc.plaintext)
			if tc.wantEncErr {
				if err == nil {
					t.Errorf("expected Encrypt to fail, but succeeded: %s", enc)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected Encrypt error: %v", err)
			}

			dec, err := crypto.Decrypt(tc.key, enc)
			if tc.wantDecErr {
				if err == nil {
					t.Errorf("expected Decrypt to fail, but succeeded")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected Decrypt error: %v", err)
			}

			if !bytes.Equal(dec, tc.plaintext) {
				t.Fatalf("decrypted plaintext mismatch: got %v, want %v", dec, tc.plaintext)
			}
		})
	}
}

func TestGenerateSalt(t *testing.T) {
	salt1, err := crypto.GenerateSalt()
	if err != nil {
		t.Fatalf("GenerateSalt failed: %v", err)
	}

	const expectedLen = 16
	if len(salt1) != expectedLen {
		t.Fatalf("expected salt length %d, got %d", expectedLen, len(salt1))
	}

	salt2, err := crypto.GenerateSalt()
	if err != nil {
		t.Fatalf("GenerateSalt second call failed: %v", err)
	}

	if bytes.Equal(salt1, salt2) {
		t.Fatalf("subsequent GenerateSalt calls produced identical salts: %x", salt1)
	}
}

func TestGenerateRandomToken(t *testing.T) {
	token1, err := crypto.GenerateRandomToken()
	if err != nil {
		t.Fatalf("GenerateRandomToken failed: %v", err)
	}

	if token1 == "" {
		t.Fatalf("GenerateRandomToken returned empty string")
	}

	// 32 bytes base64.URLEncoding = ceil(32*4/3) = 44 characters (with padding)
	if len(token1) != 44 {
		t.Errorf("expected token length 44, got %d (value: %s)", len(token1), token1)
	}

	// Check URL-safety
	escaped := url.QueryEscape(token1)
	if escaped != token1 && strings.ReplaceAll(escaped, "%3D", "=") != token1 {
		t.Errorf("token contains non-url-safe characters: %s", token1)
	}

	// Check randomness / uniqueness
	token2, err := crypto.GenerateRandomToken()
	if err != nil {
		t.Fatalf("GenerateRandomToken second call failed: %v", err)
	}

	if token1 == token2 {
		t.Fatalf("expected unique tokens, got identical tokens: %s", token1)
	}

	// Uniqueness across sample
	seen := make(map[string]bool)
	seen[token1] = true
	seen[token2] = true
	for i := 0; i < 100; i++ {
		tok, err := crypto.GenerateRandomToken()
		if err != nil {
			t.Fatalf("GenerateRandomToken failed in loop: %v", err)
		}
		if seen[tok] {
			t.Fatalf("duplicate token detected: %s", tok)
		}
		seen[tok] = true
	}
}
