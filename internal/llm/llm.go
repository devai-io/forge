// Package llm is a small client for OpenAI-compatible chat-completions APIs
// with tool calling (DeepSeek by default). It sends a whole conversation and
// returns one assistant message; the caller runs the tools and loops.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// DefaultBaseURL and DefaultModel are DeepSeek's API and its fast model.
const (
	DefaultBaseURL = "https://api.deepseek.com"
	DefaultModel   = "deepseek-flash"
)

var ErrNoKey = errors.New("no API key configured")

type Message struct {
	Role    string `json:"role"` // system | user | assistant | tool
	Content string `json:"content"`
	// ReasoningContent is DeepSeek's thinking; with tools it must be sent
	// back on every earlier assistant message.
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // "function"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON text, as the model wrote it
	} `json:"function"`
}

// Tool describes one function the model may call; Parameters is a JSON
// Schema object.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	CachedTokens int `json:"cached_tokens"`
}

type Client struct {
	BaseURL string
	Key     func(context.Context) (string, error)
	HTTP    *http.Client
}

func New(baseURL string, key func(context.Context) (string, error)) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Key: key, HTTP: &http.Client{Timeout: 5 * time.Minute}}
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	key, err := c.Key(ctx)
	if err != nil || key == "" {
		return ErrNoKey
	}
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("llm: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error struct{ Message string } `json:"error"`
		}
		msg := strings.TrimSpace(string(raw))
		if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
			msg = e.Error.Message
		}
		if len(msg) > 300 {
			msg = msg[:300]
		}
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			msg = "the API key was refused"
		case http.StatusPaymentRequired:
			msg = "the account has no balance left"
		}
		return fmt.Errorf("llm: HTTP %d: %s", resp.StatusCode, msg)
	}
	return json.Unmarshal(raw, out)
}

// Efforts are the thinking settings DeepSeek takes: "off" disables thinking,
// the others set reasoning_effort; "" leaves the provider's default.
var Efforts = []string{"off", "low", "high", "max"}

// Chat sends the conversation and returns the model's next message.
func (c *Client) Chat(ctx context.Context, model, effort string, messages []Message, tools []Tool) (Message, Usage, error) {
	body := map[string]any{"model": model, "messages": messages}
	switch effort {
	case "":
	case "off":
		body["thinking"] = map[string]string{"type": "disabled"}
	default:
		body["thinking"] = map[string]string{"type": "enabled"}
		body["reasoning_effort"] = effort
	}
	if len(tools) > 0 {
		ts := make([]map[string]any, len(tools))
		for i, t := range tools {
			ts[i] = map[string]any{"type": "function", "function": map[string]any{
				"name": t.Name, "description": t.Description, "parameters": t.Parameters}}
		}
		body["tools"] = ts
	}
	var out struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			CacheHit         int `json:"prompt_cache_hit_tokens"`
			Details          struct {
				Cached int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := c.do(ctx, http.MethodPost, "/chat/completions", body, &out); err != nil {
		return Message{}, Usage{}, err
	}
	if len(out.Choices) == 0 {
		return Message{}, Usage{}, errors.New("llm: empty answer")
	}
	u := Usage{InputTokens: out.Usage.PromptTokens, OutputTokens: out.Usage.CompletionTokens,
		CachedTokens: max(out.Usage.CacheHit, out.Usage.Details.Cached)}
	return out.Choices[0].Message, u, nil
}

// Models lists the model ids the provider offers for this key.
func (c *Client) Models(ctx context.Context) ([]string, error) {
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/models", nil, &out); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		ids = append(ids, m.ID)
	}
	sort.Strings(ids)
	return ids, nil
}
