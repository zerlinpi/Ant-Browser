package authservice

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

var rfcSecret = []byte("12345678901234567890")

func TestHOTPMatchesRFC4226Vectors(t *testing.T) {
	t.Parallel()
	// RFC 4226 appendix D.
	want := []string{"755224", "287082", "359152", "969429", "338314", "254676", "287922", "162583", "399871", "520489"}
	for counter, code := range want {
		if got := hotp(rfcSecret, uint64(counter), 6); got != code {
			t.Fatalf("counter %d: got %s want %s", counter, got, code)
		}
	}
}

func TestTOTPMatchesRFC6238Vectors(t *testing.T) {
	t.Parallel()
	// RFC 6238 appendix B, SHA1 rows (eight digits).
	vectors := []struct {
		unix int64
		code string
	}{
		{59, "94287082"}, {1111111109, "07081804"}, {1111111111, "14050471"},
		{1234567890, "89005924"}, {2000000000, "69279037"}, {20000000000, "65353130"},
	}
	secret := encodeTOTPSecret(rfcSecret)
	for _, vector := range vectors {
		if got := hotp(rfcSecret, uint64(vector.unix/totpPeriodSeconds), 8); got != vector.code {
			t.Fatalf("T=%d: got %s want %s", vector.unix, got, vector.code)
		}
		// Six-digit codes are the low digits of the same value.
		six, err := GenerateTOTPCode(secret, time.Unix(vector.unix, 0))
		if err != nil || six != vector.code[2:] {
			t.Fatalf("T=%d: six-digit code %q err=%v", vector.unix, six, err)
		}
	}
}

func TestMatchTOTPAcceptsOneStepOfSkewOnlyOnceEach(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_015, 0)
	step := now.Unix() / totpPeriodSeconds
	code := func(offset int64) string { return hotp(rfcSecret, uint64(step+offset), totpDigits) }

	for _, offset := range []int64{-1, 0, 1} {
		matched, ok := matchTOTP(rfcSecret, code(offset), now, 0)
		if !ok || matched != step+offset {
			t.Fatalf("offset %d: matched=%d ok=%v", offset, matched, ok)
		}
	}
	for _, offset := range []int64{-2, 2} {
		if _, ok := matchTOTP(rfcSecret, code(offset), now, 0); ok {
			t.Fatalf("offset %d outside the skew window was accepted", offset)
		}
	}
	// A code for an already used step is a replay.
	if _, ok := matchTOTP(rfcSecret, code(0), now, step); ok {
		t.Fatal("code for the last used step was accepted again")
	}
	if matched, ok := matchTOTP(rfcSecret, code(1), now, step); !ok || matched != step+1 {
		t.Fatalf("next step after the used one: matched=%d ok=%v", matched, ok)
	}
	for _, malformed := range []string{"", "12345", "1234567", "abcdef"} {
		if _, ok := matchTOTP(rfcSecret, malformed, now, 0); ok {
			t.Fatalf("malformed code %q accepted", malformed)
		}
	}
}

func TestTOTPSecretRoundTripsAndBuildsAnOTPAuthURI(t *testing.T) {
	t.Parallel()
	secret := encodeTOTPSecret(rfcSecret)
	if secret != "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" {
		t.Fatalf("base32 secret=%s", secret)
	}
	decoded, err := decodeTOTPSecret(strings.ToLower(secret[:8]) + " " + secret[8:] + "====")
	if err != nil || string(decoded) != string(rfcSecret) {
		t.Fatalf("decoded=%q err=%v", decoded, err)
	}
	if _, err := decodeTOTPSecret("not base32!"); err == nil {
		t.Fatal("invalid secret decoded")
	}

	uri := otpauthURI("Ant Browser", "owner+qa@example.com", secret)
	parsed, err := url.Parse(uri)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "otpauth" || parsed.Host != "totp" || parsed.Path != "/Ant Browser:owner+qa@example.com" {
		t.Fatalf("uri=%s parsed=%+v", uri, parsed)
	}
	if strings.Contains(uri, "+") {
		t.Fatalf("uri must percent-encode spaces and plus signs: %s", uri)
	}
	query := parsed.Query()
	if query.Get("secret") != secret || query.Get("issuer") != "Ant Browser" || query.Get("algorithm") != "SHA1" ||
		query.Get("digits") != "6" || query.Get("period") != "30" {
		t.Fatalf("query=%v", query)
	}
}

func TestRecoveryCodesAreUniqueFormattedAndNormalized(t *testing.T) {
	t.Parallel()
	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != recoveryCodeCount || len(hashes) != recoveryCodeCount {
		t.Fatalf("codes=%d hashes=%d", len(codes), len(hashes))
	}
	format := regexp.MustCompile(`^[a-z2-7]{4}-[a-z2-7]{4}-[a-z2-7]{4}-[a-z2-7]{4}$`)
	seen := map[string]bool{}
	for index, code := range codes {
		if !format.MatchString(code) {
			t.Fatalf("code %q has the wrong format", code)
		}
		normalized := normalizeRecoveryCode(" " + strings.ToUpper(strings.ReplaceAll(code, "-", " - ")) + " ")
		if !validRecoveryCode(normalized) || hashRecoveryCode(normalized) != hashes[index] {
			t.Fatalf("code %q normalized to %q does not match its hash", code, normalized)
		}
		if seen[hashes[index]] {
			t.Fatalf("duplicate recovery code %q", code)
		}
		seen[hashes[index]] = true
	}
	for _, invalid := range []string{"", "abcd-efgh", "abcd-efgh-ijkl-mnop-qrst", "abcd-efgh-ijkl-mn0p"} {
		if validRecoveryCode(normalizeRecoveryCode(invalid)) {
			t.Fatalf("%q accepted as a recovery code", invalid)
		}
	}
}

func TestParseSecondFactorRequiresExactlyOneProof(t *testing.T) {
	t.Parallel()
	if proof, err := parseSecondFactor(" 123 456 ", ""); err != nil || proof.code != "123456" || proof.recoveryCode != "" {
		t.Fatalf("code proof=%+v err=%v", proof, err)
	}
	if proof, err := parseSecondFactor("", "ABCD-EFGH-IJKL-MNOP"); err != nil || proof.recoveryCode != "abcdefghijklmnop" {
		t.Fatalf("recovery proof=%+v err=%v", proof, err)
	}
	for _, input := range [][2]string{{"", ""}, {"  ", " - "}, {"123456", "abcd-efgh-ijkl-mnop"}} {
		if _, err := parseSecondFactor(input[0], input[1]); err != ErrMFACodeRequired {
			t.Fatalf("parseSecondFactor(%q, %q) error=%v", input[0], input[1], err)
		}
	}
}
