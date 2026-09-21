package authservice

import (
	"crypto/sha256"
	"encoding/hex"
)

func defaultOpaqueTokenHash(raw string) string {
	digest := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(digest[:])
}
