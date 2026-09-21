// Package secureenvelope implements a small envelope-encryption boundary for
// self-hosted deployments. A random per-secret data encryption key (DEK)
// encrypts the payload; the configured master key only wraps that DEK.
package secureenvelope

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	accountservice "github.com/zerlinpi/Ant-Browser/server/services/account-service"
)

const Algorithm = "envelope-aes-256-gcm"

type Crypto struct {
	masterKey  []byte
	keyRef     string
	keyVersion string
	random     io.Reader
}

func New(masterKeyBase64, keyRef, keyVersion string) (*Crypto, error) {
	key, err := decodeKey(masterKeyBase64)
	if err != nil {
		return nil, err
	}
	keyRef, keyVersion = strings.TrimSpace(keyRef), strings.TrimSpace(keyVersion)
	if keyRef == "" || keyVersion == "" {
		return nil, errors.New("envelope key reference and version are required")
	}
	return &Crypto{masterKey: key, keyRef: keyRef, keyVersion: keyVersion, random: rand.Reader}, nil
}

func (c *Crypto) Encrypt(ctx context.Context, plaintext, aad []byte) (accountservice.SecretEnvelope, error) {
	if err := ctx.Err(); err != nil {
		return accountservice.SecretEnvelope{}, err
	}
	if len(plaintext) == 0 {
		return accountservice.SecretEnvelope{}, errors.New("plaintext is required")
	}
	dek := make([]byte, 32)
	defer clear(dek)
	if _, err := io.ReadFull(c.random, dek); err != nil {
		return accountservice.SecretEnvelope{}, fmt.Errorf("generate data key: %w", err)
	}
	payloadNonce, payloadCiphertext, err := seal(dek, plaintext, aad, c.random)
	if err != nil {
		return accountservice.SecretEnvelope{}, err
	}
	wrapAAD := []byte("ant-browser/dek/" + c.keyRef + "/" + c.keyVersion)
	// Prefix the wrapped key with its GCM nonce so it remains a single opaque
	// field in persistence and can be rotated independently of payload data.
	masterBlock, _ := aes.NewCipher(c.masterKey)
	masterGCM, _ := cipher.NewGCM(masterBlock)
	wrapNonce := make([]byte, masterGCM.NonceSize())
	if _, err := io.ReadFull(c.random, wrapNonce); err != nil {
		return accountservice.SecretEnvelope{}, fmt.Errorf("generate wrapping nonce: %w", err)
	}
	wrappedDEK := masterGCM.Seal(nil, wrapNonce, dek, wrapAAD)
	wrapped := append(wrapNonce, wrappedDEK...)

	mac := hmac.New(sha256.New, c.masterKey)
	_, _ = mac.Write(payloadCiphertext)
	return accountservice.SecretEnvelope{
		Algorithm: Algorithm, Ciphertext: encode(payloadCiphertext), Nonce: encode(payloadNonce),
		KeyReference: c.keyRef, KeyVersion: c.keyVersion, EncryptedDEK: encode(wrapped),
		Fingerprint: encode(mac.Sum(nil)),
	}, nil
}

func (c *Crypto) Decrypt(ctx context.Context, envelope accountservice.SecretEnvelope, aad []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if envelope.Algorithm != Algorithm || envelope.KeyReference != c.keyRef || envelope.KeyVersion != c.keyVersion {
		return nil, errors.New("unsupported envelope key or algorithm")
	}
	ciphertext, err := decode(envelope.Ciphertext)
	if err != nil {
		return nil, errors.New("invalid envelope ciphertext")
	}
	payloadNonce, err := decode(envelope.Nonce)
	if err != nil {
		return nil, errors.New("invalid envelope nonce")
	}
	wrapped, err := decode(envelope.EncryptedDEK)
	if err != nil {
		return nil, errors.New("invalid encrypted data key")
	}
	masterBlock, err := aes.NewCipher(c.masterKey)
	if err != nil {
		return nil, err
	}
	masterGCM, err := cipher.NewGCM(masterBlock)
	if err != nil {
		return nil, err
	}
	if len(wrapped) <= masterGCM.NonceSize() {
		return nil, errors.New("invalid encrypted data key")
	}
	wrapAAD := []byte("ant-browser/dek/" + c.keyRef + "/" + c.keyVersion)
	dek, err := masterGCM.Open(nil, wrapped[:masterGCM.NonceSize()], wrapped[masterGCM.NonceSize():], wrapAAD)
	if err != nil {
		return nil, errors.New("encrypted data key authentication failed")
	}
	defer clear(dek)
	plaintext, err := open(dek, payloadNonce, ciphertext, aad)
	if err != nil {
		return nil, errors.New("secret authentication failed")
	}
	return plaintext, nil
}

func seal(key, plaintext, aad []byte, random io.Reader) ([]byte, []byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(random, nonce); err != nil {
		return nil, nil, fmt.Errorf("generate encryption nonce: %w", err)
	}
	return nonce, gcm.Seal(nil, nonce, plaintext, aad), nil
}

func open(key, nonce, ciphertext, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, errors.New("invalid nonce length")
	}
	return gcm.Open(nil, nonce, ciphertext, aad)
}

func decodeKey(value string) ([]byte, error) {
	decoded, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(value), "="))
	if err != nil || len(decoded) != 32 {
		return nil, errors.New("master key must be base64-encoded 32-byte key material")
	}
	return decoded, nil
}

func encode(value []byte) string { return base64.RawURLEncoding.EncodeToString(value) }
func decode(value string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
}
