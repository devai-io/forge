// Package llm is a small client for OpenAI-compatible chat-completions APIs
// with tool calling (OpenRouter by default). It sends a whole conversation
// and returns one assistant message; the caller runs the tools and loops.
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

// DefaultBaseURL and DefaultModel are OpenRouter's API and DeepSeek's fast
// model there.
const (
	DefaultBaseURL = "https://openrouter.ai/api/v1"
	DefaultModel   = "deepseek/deepseek-v4-flash"
)

var ErrNoKey = errors.New("no API key configured")

type Message struct {
	Role    string `json:"role"` // system | user | assistant | tool
	Content string `json:"content"`
	// ReasoningContent is the model's thinking (DeepSeek's field; OpenRouter
	// answers it as Reasoning). With tools DeepSeek wants it sent back on
	// every earlier assistant message.
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	Reasoning        string     `json:"reasoning,omitempty"`
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
	// CostUSD is what the provider says the call cost (OpenRouter does), or nil.
	CostUSD *float64 `json:"cost_usd,omitempty"`
}

type Client struct {
	BaseURL string
	Key     func(context.Context) (string, error)
	HTTP    *http.Client
	// AppName and AppURL go out as X-Title and HTTP-Referer, so OpenRouter
	// splits the key's spend per app.
	AppName, AppURL string
}

// App sets the attribution headers and returns c.
func (c *Client) App(name, url string) *Client {
	c.AppName, c.AppURL = name, url
	return c
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
	if c.AppName != "" {
		req.Header.Set("X-Title", c.AppName)
	}
	if c.AppURL != "" {
		req.Header.Set("HTTP-Referer", c.AppURL)
	}
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

// Efforts are the thinking settings a chat can pick: "off" (also what ""
// means: DeepSeek on OpenRouter reasons by default, which a chat rarely
// needs) or how hard to think.
var Efforts = []string{"off", "low", "high", "max"}

// Chat sends the conversation and returns the model's next message.
func (c *Client) Chat(ctx context.Context, model, effort string, messages []Message, tools []Tool) (Message, Usage, error) {
	body := map[string]any{"model": model, "messages": messages}
	switch effort {
	case "", "off":
		body["reasoning"] = map[string]any{"enabled": false}
	case "max":
		body["reasoning"] = map[string]any{"effort": "xhigh"}
	default:
		body["reasoning"] = map[string]any{"effort": effort}
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
			Cost *float64 `json:"cost"`
		} `json:"usage"`
	}
	if err := c.do(ctx, http.MethodPost, "/chat/completions", body, &out); err != nil {
		return Message{}, Usage{}, err
	}
	if len(out.Choices) == 0 {
		return Message{}, Usage{}, errors.New("llm: empty answer")
	}
	u := Usage{InputTokens: out.Usage.PromptTokens, OutputTokens: out.Usage.CompletionTokens,
		CachedTokens: max(out.Usage.CacheHit, out.Usage.Details.Cached), CostUSD: out.Usage.Cost}
	msg := out.Choices[0].Message
	if msg.ReasoningContent == "" {
		msg.ReasoningContent = msg.Reasoning
	}
	msg.Reasoning = ""
	return msg, u, nil
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
