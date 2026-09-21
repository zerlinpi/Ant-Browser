package security

import (
	"errors"
	"strings"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

var ErrWeakPassword = errors.New("password must be at least 12 characters and contain letters and numbers")

type Passwords struct {
	Cost int
}

func NewPasswords() Passwords {
	return Passwords{Cost: bcrypt.DefaultCost}
}

func (p Passwords) Validate(plain string) error {
	if len([]rune(plain)) < 12 || strings.TrimSpace(plain) != plain {
		return ErrWeakPassword
	}
	var letter, number bool
	for _, value := range plain {
		letter = letter || unicode.IsLetter(value)
		number = number || unicode.IsNumber(value)
	}
	if !letter || !number {
		return ErrWeakPassword
	}
	return nil
}

func (p Passwords) Hash(plain string) (string, error) {
	if err := p.Validate(plain); err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), p.Cost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func (p Passwords) Compare(hash, plain string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain))
}
