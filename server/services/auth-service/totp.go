package authservice

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // RFC 6238 TOTP is defined over HMAC-SHA1, the variant every authenticator app implements.
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// TOTP parameters: the RFC 6238 defaults, which every authenticator app
// supports without extra configuration.
const (
	totpAlgorithm     = "SHA1"
	totpPeriodSeconds = 30
	totpDigits        = 6
	// totpSkew accepts the codes of one step before and after the current
	// one to absorb clock drift and typing time.
	totpSkew = 1
	// totpSecretBytes is the RFC 4226 recommended 160-bit key length.
	totpSecretBytes = 20
)

// Recovery codes are 16 base32 characters (80 random bits) shown as four
// dash-separated groups. They are single use and stored only as hashes.
const (
	recoveryCodeCount  = 10
	recoveryCodeBytes  = 10
	recoveryCodeLength = 16
)

var (
	totpEncoding     = base32.StdEncoding.WithPadding(base32.NoPadding)
	recoveryEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)
)

// hotp is RFC 4226 HOTP with dynamic truncation to digits decimal digits.
func hotp(secret []byte, counter uint64, digits int) string {
	var message [8]byte
	binary.BigEndian.PutUint64(message[:], counter)
	mac := hmac.New(sha1.New, secret)
	_, _ = mac.Write(message[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := uint64(binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff)
	modulus := uint64(1)
	for range digits {
		modulus *= 10
	}
	code := strconv.FormatUint(value%modulus, 10)
	return strings.Repeat("0", digits-len(code)) + code
}

// matchTOTP returns the earliest time step within totpSkew of now whose code
// equals code and that is later than after, the last step already used.
// Requiring a later step makes every code single use. All candidates are
// computed and compared in constant time.
func matchTOTP(secret []byte, code string, now time.Time, after int64) (int64, bool) {
	if len(code) != totpDigits {
		return 0, false
	}
	current := now.Unix() / totpPeriodSeconds
	var matched int64
	found := false
	for step := current - totpSkew; step <= current+totpSkew; step++ {
		if step < 0 {
			continue
		}
		equal := subtle.ConstantTimeCompare([]byte(hotp(secret, uint64(step), totpDigits)), []byte(code)) == 1
		if equal && step > after && !found {
			matched, found = step, true
		}
	}
	return matched, found
}

// GenerateTOTPCode returns the current code for a base32 secret as returned
// by SetupTOTP. It is the authenticator-app half of the algorithm, used by
// tests and tooling; the service never needs it.
func GenerateTOTPCode(secret string, at time.Time) (string, error) {
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return "", err
	}
	defer clearBytes(key)
	return hotp(key, uint64(at.Unix()/totpPeriodSeconds), totpDigits), nil
}

func encodeTOTPSecret(secret []byte) string {
	return totpEncoding.EncodeToString(secret)
}

func decodeTOTPSecret(value string) ([]byte, error) {
	value = strings.ToUpper(strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(value), " ", ""), "="))
	key, err := totpEncoding.DecodeString(value)
	if err != nil || len(key) == 0 {
		return nil, errors.New("totp secret must be base32")
	}
	return key, nil
}

// otpauthURI is the Key URI Format understood by authenticator apps:
// otpauth://totp/Issuer:account?secret=...&issuer=Issuer&...
func otpauthURI(issuer, account, secret string) string {
	return "otpauth://totp/" + uriEscape(issuer) + ":" + uriEscape(account) +
		"?secret=" + secret + "&issuer=" + uriEscape(issuer) +
		"&algorithm=" + totpAlgorithm + "&digits=" + strconv.Itoa(totpDigits) +
		"&period=" + strconv.Itoa(totpPeriodSeconds)
}

// uriEscape percent-encodes spaces as %20; some authenticator apps show a
// literal "+" otherwise.
func uriEscape(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

// normalizeTOTPCode drops the spaces and dashes users type or paste between
// digit groups.
func normalizeTOTPCode(value string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '\t' {
			return -1
		}
		return r
	}, strings.TrimSpace(value))
}

// newRecoveryCodes returns fresh display codes and their storage hashes.
func newRecoveryCodes() (codes, hashes []string, err error) {
	seen := make(map[string]struct{}, recoveryCodeCount)
	raw := make([]byte, recoveryCodeBytes)
	defer clearBytes(raw)
	for len(codes) < recoveryCodeCount {
		if _, err := rand.Read(raw); err != nil {
			return nil, nil, err
		}
		normalized := recoveryEncoding.EncodeToString(raw)
		if _, duplicate := seen[normalized]; duplicate {
			continue
		}
		seen[normalized] = struct{}{}
		codes = append(codes, normalized[0:4]+"-"+normalized[4:8]+"-"+normalized[8:12]+"-"+normalized[12:16])
		hashes = append(hashes, hashRecoveryCode(normalized))
	}
	return codes, hashes, nil
}

// normalizeRecoveryCode accepts any letter case and ignores the separators
// users type or paste.
func normalizeRecoveryCode(value string) string {
	return strings.ToLower(normalizeTOTPCode(value))
}

// hashRecoveryCode hashes a normalized recovery code. Codes carry 80 random
// bits, so a fast hash is not brute-forceable; the prefix keeps the digest
// distinct from other SHA-256 token hashes in the database.
func hashRecoveryCode(normalized string) string {
	digest := sha256.Sum256([]byte("ant-browser/mfa-recovery/" + normalized))
	return hex.EncodeToString(digest[:])
}

func validRecoveryCode(normalized string) bool {
	if len(normalized) != recoveryCodeLength {
		return false
	}
	_, err := recoveryEncoding.DecodeString(normalized)
	return err == nil
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
