package postgres

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSuffixedNameRespectsTheNameLimit(t *testing.T) {
	for _, tc := range []struct {
		name string
		k    int
		want string
	}{
		{"Shop 1", 2, "Shop 1 (2)"},
		{"nightly", 13, "nightly (13)"},
		{strings.Repeat("a", 120), 2, strings.Repeat("a", 116) + " (2)"},
		{strings.Repeat("界", 119), 10, strings.Repeat("界", 115) + " (10)"},
		// A cut that ends in spaces does not leave them before the suffix.
		{"abc" + strings.Repeat(" ", 114) + "tail", 3, "abc (3)"},
	} {
		got := suffixedName(tc.name, tc.k, preflightNameLimit)
		if got != tc.want {
			t.Errorf("suffixedName(%q, %d) = %q, want %q", tc.name, tc.k, got, tc.want)
		}
		if count := utf8.RuneCountInString(got); count > preflightNameLimit {
			t.Errorf("suffixedName(%q, %d) has %d characters", tc.name, tc.k, count)
		}
	}
}

func TestMaskIdentifierKeepsAtMostTwoCharactersAndTheDomain(t *testing.T) {
	for identifier, want := range map[string]string{
		"seller@example.test": "se***@example.test",
		"ab@example.test":     "a***@example.test",
		"a@example.test":      "***@example.test",
		"creator42":           "cr***",
		"ab":                  "a***",
		"@handle":             "@h***",
		"user@":               "us***",
		"":                    "***",
		"用户名@例子.测试":           "用户***@例子.测试",
		"first@second@x.io":   "fi***@x.io",
	} {
		if got := maskIdentifier(identifier); got != want {
			t.Errorf("maskIdentifier(%q) = %q, want %q", identifier, got, want)
		}
	}
}
