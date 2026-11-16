package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTruncatedAnswerRetainsBilledUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"finish_reason":"length","message":{"content":"partial summary"}}],"usage":{"prompt_tokens":100,"completion_tokens":20,"cost":0.01}}`))
	}))
	defer server.Close()
	c := New(server.URL, func(context.Context) (string, error) { return "test", nil })
	recorded := false
	c.Observe = func(_ context.Context, _, _ string, u Usage, err error) {
		recorded = true
		if err == nil || u.InputTokens != 100 || u.CostUSD == nil {
			t.Errorf("lost billed failure: %+v %v", u, err)
		}
	}
	msg, _, err := c.Chat(context.Background(), "test", "off", nil, nil)
	if err == nil || msg.Content != "" || !recorded {
		t.Fatal("partial answer accepted or not recorded")
	}
}
