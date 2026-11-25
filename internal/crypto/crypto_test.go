package crypto_test

import (
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
