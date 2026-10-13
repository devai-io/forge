package jev

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/devai-io/forge/internal/usage"
)

func TestAsk(t *testing.T) {
	answer := `{"q":0.8}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k-123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("X-Title") != "Forge" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body struct {
			Model          string
			ResponseFormat struct {
				JSONSchema struct {
					Schema struct {
						Required []string
					}
				} `json:"json_schema"`
			} `json:"response_format"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Model != DefaultModel || len(body.ResponseFormat.JSONSchema.Schema.Required) == 0 {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		raw, _ := json.Marshal(answer)
		_, _ = w.Write([]byte(`{"model":"some/model","choices":[{"message":{"content":` + string(raw) +
			`}}],"usage":{"prompt_tokens":42,"completion_tokens":5,"cost":0.0001}}`))
	}))
	defer srv.Close()
	c := New(func(context.Context) (string, error) { return "k-123", nil },
		func(context.Context) Config { return Config{BaseURL: srv.URL + "/v1/"} })
	meter := usage.NewMeter()
	ctx := usage.WithMeter(context.Background(), meter)
	ans, err := c.Ask(ctx, "state", map[string]Question{"q": Noul("?")})
	if err != nil || ans["q"].Noul != 0.8 {
		t.Fatalf("ask: %v %+v", err, ans)
	}
	if es := meter.Entries(); len(es) != 1 || es[0].API != "openrouter" || es[0].Model != DefaultModel ||
		es[0].CostUSD == nil || *es[0].CostUSD != 0.0001 || es[0].Note != "Jev → some/model" {
		t.Fatalf("usage = %+v", es)
	}
	if _, err := c.Ask(context.Background(), "state", map[string]Question{"missing": Noul("?")}); err == nil ||
		!strings.Contains(err.Error(), "no answer") {
		t.Fatalf("missing answer: %v", err)
	}
	answer = "```json\n{\"pick\":{\"choice\":\"b\",\"confidence\":1.4}}\n```"
	ans, err = c.Ask(context.Background(), "state", map[string]Question{"pick": Choice("?", map[string]string{"a": "A", "b": "B"})})
	if err != nil || ans["pick"].Choice != "b" || ans["pick"].Confidence != 1 {
		t.Fatalf("choice: %v %+v", err, ans)
	}
	answer = `{"pick":{"choice":"z","confidence":0.9}}`
	if _, err := c.Ask(context.Background(), "state", map[string]Question{"pick": Choice("?", map[string]string{"a": "A"})}); err == nil {
		t.Fatal("an answer outside the options must fail")
	}
	st := c.Stats()
	if st.Calls != 4 || st.Errors != 2 || st.InputTokens != 168 || st.LastModel != "some/model" {
		t.Fatalf("stats = %+v", st)
	}
	noKey := New(func(context.Context) (string, error) { return "", nil }, func(context.Context) Config { return Config{} })
	if _, err := noKey.Ask(context.Background(), "", nil); !errors.Is(err, ErrNoKey) {
		t.Fatalf("no key: %v", err)
	}
}
