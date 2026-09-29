package secureenvelope

import (
	"context"
	"encoding/json"
	"errors"

	accountservice "github.com/zerlinpi/Ant-Browser/server/services/account-service"
)

// TextSealer stores an envelope as one self-describing text value, for small
// secrets kept in a single column such as TOTP seeds. It satisfies
// authservice.SecretSealer.
type TextSealer struct {
	crypto *Crypto
}

func NewTextSealer(crypto *Crypto) (*TextSealer, error) {
	if crypto == nil {
		return nil, errors.New("envelope crypto is required")
	}
	return &TextSealer{crypto: crypto}, nil
}

// sealedText is the persisted layout. SecretEnvelope hides its ciphertext
// fields from JSON because it doubles as API metadata, so it cannot be
// marshalled directly.
type sealedText struct {
	Version      int    `json:"v"`
	Algorithm    string `json:"alg"`
	KeyReference string `json:"kid"`
	KeyVersion   string `json:"kv"`
	EncryptedDEK string `json:"dek"`
	Nonce        string `json:"iv"`
	Ciphertext   string `json:"ct"`
}

const sealedTextVersion = 1

func (s *TextSealer) Seal(ctx context.Context, plaintext, aad []byte) (string, error) {
	envelope, err := s.crypto.Encrypt(ctx, plaintext, aad)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(sealedText{
		Version: sealedTextVersion, Algorithm: envelope.Algorithm,
		KeyReference: envelope.KeyReference, KeyVersion: envelope.KeyVersion,
		EncryptedDEK: envelope.EncryptedDEK, Nonce: envelope.Nonce, Ciphertext: envelope.Ciphertext,
	})
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func (s *TextSealer) Open(ctx context.Context, sealed string, aad []byte) ([]byte, error) {
	var value sealedText
	if err := json.Unmarshal([]byte(sealed), &value); err != nil || value.Version != sealedTextVersion {
		return nil, errors.New("invalid sealed value")
	}
	return s.crypto.Decrypt(ctx, accountservice.SecretEnvelope{
		Algorithm: value.Algorithm, KeyReference: value.KeyReference, KeyVersion: value.KeyVersion,
		EncryptedDEK: value.EncryptedDEK, Nonce: value.Nonce, Ciphertext: value.Ciphertext,
	}, aad)
}
