package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// RFC 6238 TOTP: 30-second steps, 6 digits, HMAC-SHA1 — what every
// authenticator app speaks by default.

const totpStep = 30

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func NewTOTPSecret() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b32.EncodeToString(b)
}

func TOTPURL(secret, account, issuer string) string {
	v := url.Values{}
	v.Set("secret", secret)
	v.Set("issuer", issuer)
	v.Set("algorithm", "SHA1")
	v.Set("digits", "6")
	v.Set("period", fmt.Sprint(totpStep))
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?" + v.Encode()
}

func totpCode(key []byte, counter uint64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", bin%1_000_000)
}

// TOTPCode is the code for time t (exported for tests and the CLI).
func TOTPCode(secret string, t time.Time) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", err
	}
	return totpCode(key, uint64(t.Unix()/totpStep)), nil
}

// VerifyTOTP accepts the current step and one either side (clock drift) and
// returns the matched counter, which the caller stores so the same code
// cannot be replayed. A counter <= lastCounter is refused.
func VerifyTOTP(secret, code string, now time.Time, lastCounter int64) (int64, bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != 6 {
		return 0, false
	}
	key, err := b32.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return 0, false
	}
	cur := now.Unix() / totpStep
	for _, c := range []int64{cur - 1, cur, cur + 1} {
		if c <= lastCounter {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(totpCode(key, uint64(c))), []byte(code)) == 1 {
			return c, true
		}
	}
	return 0, false
}
