package secureenvelope

import (
	"context"
	"encoding/base64"
	"testing"
)

func TestEnvelopeRoundTripAndAADBinding(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
	crypto, err := New(key, "kms://test/account-secrets", "v7")
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := crypto.Encrypt(context.Background(), []byte("seller-password"), []byte("workspace/account/password"))
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Ciphertext == "seller-password" || envelope.EncryptedDEK == "" || envelope.Fingerprint == "" {
		t.Fatalf("unsafe or incomplete envelope: %+v", envelope)
	}
	plaintext, err := crypto.Decrypt(context.Background(), envelope, []byte("workspace/account/password"))
	if err != nil {
		t.Fatal(err)
	}
	if string(plaintext) != "seller-password" {
		t.Fatalf("plaintext = %q", plaintext)
	}
	if _, err := crypto.Decrypt(context.Background(), envelope, []byte("another-account")); err == nil {
		t.Fatal("envelope decrypted with different associated data")
	}
}

func TestRejectsInvalidMasterKey(t *testing.T) {
	if _, err := New("short", "kms://test", "v1"); err == nil {
		t.Fatal("invalid key was accepted")
	}
}

func TestEnvelopeRejectsTamperingAndWrongMasterKey(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
	crypto, err := New(key, "local://secrets", "v1")
	if err != nil {
		t.Fatal(err)
	}
	original, err := crypto.Encrypt(context.Background(), []byte("credential"), []byte("tenant/subject"))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"ciphertext", "nonce", "dek", "version"} {
		t.Run(field, func(t *testing.T) {
			modified := original
			switch field {
			case "ciphertext":
				bytes, _ := decode(modified.Ciphertext)
				bytes[0] ^= 1
				modified.Ciphertext = encode(bytes)
			case "nonce":
				modified.Nonce = encode([]byte("short"))
			case "dek":
				bytes, _ := decode(modified.EncryptedDEK)
				bytes[len(bytes)-1] ^= 1
				modified.EncryptedDEK = encode(bytes)
			case "version":
				modified.KeyVersion = "v2"
			}
			if _, err := crypto.Decrypt(context.Background(), modified, []byte("tenant/subject")); err == nil {
				t.Fatal("tampered envelope accepted")
			}
		})
	}
	other, err := New(base64.StdEncoding.EncodeToString([]byte("abcdefghijklmnopqrstuvwxyz012345")), "local://secrets", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Decrypt(context.Background(), original, []byte("tenant/subject")); err == nil {
		t.Fatal("wrong master key accepted")
	}
}
