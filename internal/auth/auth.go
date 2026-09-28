// Package auth holds the credential primitives: password hashing, random
// tokens, and the hash a token is stored under.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"math/big"
	"strings"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

const bcryptCost = 12

const MinPasswordLength = 12

func HashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcryptCost)
	return string(h), err
}

func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// dummyHash lets a login for an unknown user cost the same as a wrong
// password, so response time does not reveal which usernames exist.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("forge-timing-equaliser"), bcryptCost)

func BurnPasswordCheck(pw string) { _ = bcrypt.CompareHashAndPassword(dummyHash, []byte(pw)) }

// ValidatePassword enforces length and rejects the obviously weak. bcrypt
// silently ignores bytes past 72, so longer passwords are refused rather than
// half-checked.
func ValidatePassword(pw string) error {
	if len(pw) < MinPasswordLength {
		return errors.New("must be at least 12 characters")
	}
	if len(pw) > 72 {
		return errors.New("must be at most 72 bytes")
	}
	if strings.TrimSpace(pw) == "" {
		return errors.New("must not be blank")
	}
	distinct := map[rune]bool{}
	for _, r := range pw {
		distinct[unicode.ToLower(r)] = true
	}
	if len(distinct) < 6 {
		return errors.New("is too repetitive")
	}
	return nil
}

// Token returns a random URL-safe token with the given prefix and its storage
// hash. Only the hash is ever written down.
func Token(prefix string) (token string, hash []byte) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the OS CSPRNG failing is not a recoverable condition
	}
	token = prefix + base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token)
}

func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// GeneratePassword returns a random password from an alphabet without
// look-alikes (no 0/O, 1/l/I), grouped for reading off a screen.
func GeneratePassword() string {
	const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	var sb strings.Builder
	for i := 0; i < 24; i++ {
		if i > 0 && i%6 == 0 {
			sb.WriteByte('-')
		}
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			panic(err)
		}
		sb.WriteByte(alphabet[n.Int64()])
	}
	return sb.String()
}

// pairAlphabet has no 0/O, 1/I/L or U: a pairing code is read off one screen
// and typed on another machine.
const pairAlphabet = "ABCDEFGHJKMNPQRSTVWXYZ23456789"

// PairCode is a one-time machine pairing code, "XXXX-XXXX" (~39 bits; it
// lives for minutes and the endpoint that takes it is rate-limited).
func PairCode() string {
	var sb strings.Builder
	for i := 0; i < 8; i++ {
		if i == 4 {
			sb.WriteByte('-')
		}
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(pairAlphabet))))
		if err != nil {
			panic(err)
		}
		sb.WriteByte(pairAlphabet[n.Int64()])
	}
	return sb.String()
}

// NormalizePairCode is what gets hashed: upper case, no separators, so
// "abcd efgh" and "ABCD-EFGH" are the same code.
func NormalizePairCode(code string) string {
	var sb strings.Builder
	for _, r := range strings.ToUpper(code) {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}
