package auth

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

func TestGeneratedPasswordsAreStrongAndReadable(t *testing.T) {
	re := regexp.MustCompile(`^[a-km-zA-HJ-NP-Z2-9]{6}(-[a-km-zA-HJ-NP-Z2-9]{6}){3}$`)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		pw := GeneratePassword()
		if !re.MatchString(pw) {
			t.Fatalf("%q does not match the expected shape", pw)
		}
		if err := ValidatePassword(pw); err != nil {
			t.Fatalf("%q fails our own policy: %v", pw, err)
		}
		if seen[pw] {
			t.Fatal("duplicate password")
		}
		seen[pw] = true
	}
}

func TestValidatePassword(t *testing.T) {
	for _, bad := range []string{"short", "aaaaaaaaaaaaaaaa", strings.Repeat("ab1", 30), "            "} {
		if ValidatePassword(bad) == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
	if err := ValidatePassword("correct horse battery"); err != nil {
		t.Error(err)
	}
}

func TestHashAndCheck(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(h, "correct horse battery") || CheckPassword(h, "correct horse batterY") {
		t.Error("CheckPassword")
	}
}

func TestToken(t *testing.T) {
	tok, hash := Token("frg_")
	if !strings.HasPrefix(tok, "frg_") || len(tok) < 40 {
		t.Fatalf("token %q", tok)
	}
	if !bytes.Equal(hash, HashToken(tok)) {
		t.Error("the stored hash must be the hash of the token")
	}
	other, _ := Token("frg_")
	if other == tok {
		t.Error("tokens must be random")
	}
}

func TestPairCode(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		c := PairCode()
		if len(c) != 9 || c[4] != '-' {
			t.Fatalf("shape: %q", c)
		}
		if strings.ContainsAny(c, "01ILOU") {
			t.Fatalf("ambiguous character in %q", c)
		}
		seen[c] = true
	}
	if len(seen) < 199 {
		t.Fatalf("codes repeat: %d distinct of 200", len(seen))
	}
	if got := NormalizePairCode(" abcd-efgh\n"); got != "ABCDEFGH" {
		t.Fatalf("normalize: %q", got)
	}
}
