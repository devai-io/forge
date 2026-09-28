package jev

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAsk(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k-123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body struct {
			Model     string
			Questions map[string]Question
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Model != Model || len(body.Questions) == 0 {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		_, _ = w.Write([]byte(`{"model":"jev-1","answers":{"q":{"type":"noul","noul":0.8}},"usage":{"input_tokens":42}}`))
	}))
	defer srv.Close()
	c := New(func(context.Context) (string, error) { return "k-123", nil })
	c.URL = srv.URL
	ans, err := c.Ask(context.Background(), "state", map[string]Question{"q": Noul("?")})
	if err != nil || ans["q"].Noul != 0.8 {
		t.Fatalf("ask: %v %+v", err, ans)
	}
	if _, err := c.Ask(context.Background(), "state", map[string]Question{"missing": Noul("?")}); err == nil ||
		!strings.Contains(err.Error(), "no answer") {
		t.Fatalf("missing answer: %v", err)
	}
	st := c.Stats()
	if st.Calls != 2 || st.Errors != 1 || st.InputTokens != 84 || st.LastModel != "jev-1" {
		t.Fatalf("stats = %+v", st)
	}
	noKey := New(func(context.Context) (string, error) { return "", nil })
	if _, err := noKey.Ask(context.Background(), "", nil); !errors.Is(err, ErrNoKey) {
		t.Fatalf("no key: %v", err)
	}
}
