package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/devai-io/forge/internal/db"
	"github.com/devai-io/forge/internal/llm"
	"github.com/devai-io/forge/internal/store"
	"github.com/devai-io/forge/internal/usage"
)

func TestEarlySummaryUsesMeasuredBudgetAndKeepsHistory(t *testing.T) {
	for _, truncated := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "truncated"}[truncated], func(t *testing.T) {
			ctx := context.Background()
			database, err := db.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			if err := db.Migrate(ctx, database); err != nil {
				t.Fatal(err)
			}
			st := store.New(database)
			a := New(st, Hooks{})
			chat, err := st.CreateChat(ctx, "Long conversation", store.ChatSettings{Engine: "deepseek", Model: "expensive-model"})
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 5; i++ {
				for _, m := range sampleTurn(i*4+1, 2500) {
					if _, err := st.AppendChatMessage(ctx, chat.ID, m); err != nil {
						t.Fatal(err)
					}
				}
			}
			st.RecordUsage(ctx, nil, usage.Scope{Feature: "assistant", ChatID: &chat.ID}, usage.Entry{API: "test", Model: "test", Calls: 1, InputTokens: 25000}, "unknown", false)
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var req struct {
					Model     string
					MaxTokens int `json:"max_tokens"`
				}
				json.NewDecoder(r.Body).Decode(&req)
				if req.Model != store.DefaultModel || req.MaxTokens != 2000 {
					t.Errorf("summary policy: %+v", req)
				}
				finish := "stop"
				if truncated {
					finish = "length"
				}
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": finish, "message": map[string]string{"content": "Decided to keep API compatibility. Next: verify tests."}}}, "usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 20, "cost": 0.01}})
			}))
			defer server.Close()
			c := llm.New(server.URL, func(context.Context) (string, error) { return "test", nil })
			c.Observe = st.ObserveLLM
			a.fold(usage.WithScope(ctx, usage.Scope{Feature: "assistant", ChatID: &chat.ID}), c, "expensive-model", "test", chat, usage.NewMeter())
			if calls != 1 {
				t.Fatalf("measured budget failed to trigger: %d", calls)
			}
			if truncated && (chat.SummarySeq != 0 || chat.Summary != "") {
				t.Fatal("truncated summary replaced history")
			}
			if !truncated && (chat.SummarySeq == 0 || chat.Summary == "") {
				t.Fatal("complete summary not saved")
			}
			messages, err := st.ChatMessages(ctx, chat.ID, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(messages) != 20 {
				t.Fatalf("history deleted: %d", len(messages))
			}
			if !truncated {
				remaining, err := st.ChatMessages(ctx, chat.ID, chat.SummarySeq)
				if err != nil {
					t.Fatal(err)
				}
				if len(remaining) == 0 || remaining[0].Role != "user" {
					t.Fatal("cut split tool call/response")
				}
			}
			groups, err := st.AIUsage(ctx, 30)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, g := range groups {
				if g.Feature == "summary" {
					found = true
					if g.CostUSD == nil || *g.CostUSD != 0.01 {
						t.Fatal("provider cost lost")
					}
					if truncated && g.Failed != 1 {
						t.Fatal("failure missing")
					}
				}
			}
			if !found {
				t.Fatal("summary not attributed")
			}
		})
	}
}
