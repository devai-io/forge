// Package jev asks TypeSafe's Jev, through the provider's (OpenRouter's)
// chat-completions API, typed questions: yes/no as a probability, or one of
// a set of choices with a confidence. The answers come back as JSON in a
// fixed schema. Forge uses it where a routine judgement would otherwise
// cost frontier-model tokens: which model a run needs, which open tasks a
// Claude session should be told about. Callers keep the state they send
// short.
package jev

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
	"sync"
	"time"

	"github.com/devai-io/forge/internal/usage"
)

// DefaultModel is TypeSafe's Jev router on OpenRouter.
const DefaultModel = "typesafe/jev-router"

// ErrNoKey means no provider key is configured (the vault item tagged
// integration:openrouter).
var ErrNoKey = errors.New("no OpenRouter API key configured")

// Question is one typed question. Type "noul" answers a probability that the
// instructions hold; "choice" picks one of Criteria's keys (each described by
// its value).
type Question struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria,omitempty"`
}

// Noul asks a yes/no question.
func Noul(instructions string) Question { return Question{Type: "noul", Instructions: instructions} }

// Choice asks for one of the options (name → what it means).
func Choice(instructions string, options map[string]string) Question {
	return Question{Type: "choice", Instructions: instructions, Criteria: options}
}

type Answer struct {
	Type          string             `json:"type"`
	Noul          float64            `json:"noul"`
	Choice        string             `json:"choice"`
	Confidence    float64            `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
}

// Stats counts calls since the server started, for the Settings page.
type Stats struct {
	Calls       int        `json:"calls"`
	Errors      int        `json:"errors"`
	InputTokens int        `json:"input_tokens"`
	LastAt      *time.Time `json:"last_at"`
	LastError   string     `json:"last_error"`
	LastModel   string     `json:"last_model"`
}

// Config is where the questions go: the provider's OpenAI-compatible base
// URL and the Jev model there.
type Config struct {
	BaseURL string
	Model   string
}

type Client struct {
	// Key returns the API key and Config the endpoint, both read on each
	// call so new settings apply without a restart.
	Key    func(context.Context) (string, error)
	Config func(context.Context) Config
	// AppName and AppURL attribute the spend (X-Title, HTTP-Referer).
	AppName, AppURL string
	HTTP            *http.Client

	mu    sync.Mutex
	stats Stats
}

func New(key func(context.Context) (string, error), cfg func(context.Context) Config) *Client {
	return &Client{Key: key, Config: cfg, AppName: "Forge", HTTP: &http.Client{Timeout: 10 * time.Second}}
}

// Ask sends state (any JSON value; keep it small) and the named questions,
// and returns the answers by name.
func (c *Client) Ask(ctx context.Context, state any, questions map[string]Question) (map[string]Answer, error) {
	answers, err := c.ask(ctx, state, questions)
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now().UTC()
	c.stats.Calls++
	c.stats.LastAt = &now
	if err != nil {
		c.stats.Errors++
		c.stats.LastError = err.Error()
	} else {
		c.stats.LastError = ""
	}
	return answers, err
}

const instructions = `You answer typed questions about the state the user sends. Reply with JSON only, matching the schema:
- a "noul" question: a number from 0 to 1, the probability that its statement holds;
- a "choice" question: {"choice": one of its options, "confidence": a number from 0 to 1}.`

// request builds the chat-completions body: the questions as text, and a
// strict JSON schema with one property per question.
func request(model string, state any, questions map[string]Question) (map[string]any, error) {
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(questions))
	for n := range questions {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("State:\n" + string(stateJSON) + "\n\nQuestions:\n")
	props := map[string]any{}
	for _, n := range names {
		q := questions[n]
		switch q.Type {
		case "choice":
			opts := make([]string, 0, len(q.Criteria))
			for k := range q.Criteria {
				opts = append(opts, k)
			}
			sort.Strings(opts)
			fmt.Fprintf(&b, "- %s (choice): %s\n", n, q.Instructions)
			for _, o := range opts {
				fmt.Fprintf(&b, "    %s: %s\n", o, q.Criteria[o])
			}
			props[n] = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"choice", "confidence"},
				"properties": map[string]any{"choice": map[string]any{"type": "string", "enum": opts}, "confidence": map[string]any{"type": "number"}}}
		default:
			fmt.Fprintf(&b, "- %s (noul): %s\n", n, q.Instructions)
			props[n] = map[string]any{"type": "number"}
		}
	}
	return map[string]any{
		"model": model,
		// The router may pick a model that thinks first whatever "reasoning"
		// says; too small a budget and the answer is cut off mid-thought.
		"max_tokens": min(512+16*len(names), 4096),
		"reasoning":  map[string]any{"enabled": false},
		"messages": []map[string]string{
			{"role": "system", "content": instructions},
			{"role": "user", "content": b.String()},
		},
		"response_format": map[string]any{"type": "json_schema", "json_schema": map[string]any{
			"name": "answers", "strict": true, "schema": map[string]any{
				"type": "object", "properties": props, "required": names, "additionalProperties": false}}},
	}, nil
}

func (c *Client) ask(ctx context.Context, state any, questions map[string]Question) (map[string]Answer, error) {
	key, err := c.Key(ctx)
	if err != nil || key == "" {
		return nil, ErrNoKey
	}
	cfg := c.Config(ctx)
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	reqBody, err := request(cfg.Model, state, questions)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
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
		return nil, fmt.Errorf("jev: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		msg := string(raw)
		var e struct {
			Error struct{ Message string } `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
			msg = e.Error.Message
		}
		if len(msg) > 200 {
			msg = msg[:200]
		}
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			msg = "the API key was refused"
		case http.StatusPaymentRequired:
			msg = "the account has no balance left"
		}
		return nil, fmt.Errorf("jev: HTTP %d: %s", resp.StatusCode, msg)
	}
	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int64    `json:"prompt_tokens"`
			CompletionTokens int64    `json:"completion_tokens"`
			Cost             *float64 `json:"cost"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Choices) == 0 {
		return nil, errors.New("jev: malformed response")
	}
	c.mu.Lock() // billed, whether or not the answers are usable
	c.stats.InputTokens += int(out.Usage.PromptTokens)
	c.stats.LastModel = out.Model
	c.mu.Unlock()
	e := usage.Entry{API: "openrouter", Model: cfg.Model, Calls: 1, InputTokens: out.Usage.PromptTokens,
		OutputTokens: out.Usage.CompletionTokens, CostUSD: out.Usage.Cost}
	if out.Model != "" && out.Model != cfg.Model {
		e.Note = "Jev → " + out.Model
	}
	usage.From(ctx).Add(e)
	return parse(out.Choices[0].Message.Content, questions)
}

// parse reads the answers from the model's JSON (tolerating a code fence or
// text around it).
func parse(content string, questions map[string]Question) (map[string]Answer, error) {
	if i, j := strings.Index(content, "{"), strings.LastIndex(content, "}"); i >= 0 && j > i {
		content = content[i : j+1]
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &got); err != nil {
		return nil, errors.New("jev: the answer was not JSON")
	}
	clamp := func(v float64) float64 { return min(max(v, 0), 1) }
	answers := map[string]Answer{}
	for name, q := range questions {
		v, ok := got[name]
		if !ok {
			return nil, fmt.Errorf("jev: no answer for %q", name)
		}
		switch q.Type {
		case "choice":
			var a struct {
				Choice     string  `json:"choice"`
				Confidence float64 `json:"confidence"`
			}
			if err := json.Unmarshal(v, &a); err != nil {
				return nil, fmt.Errorf("jev: bad answer for %q", name)
			}
			if _, ok := q.Criteria[a.Choice]; !ok {
				return nil, fmt.Errorf("jev: %q is not an option of %q", a.Choice, name)
			}
			answers[name] = Answer{Type: "choice", Choice: a.Choice, Confidence: clamp(a.Confidence)}
		default:
			var p float64
			if err := json.Unmarshal(v, &p); err != nil {
				var b bool // a model may answer true/false
				if json.Unmarshal(v, &b) != nil {
					return nil, fmt.Errorf("jev: bad answer for %q", name)
				}
				p = map[bool]float64{true: 1, false: 0}[b]
			}
			answers[name] = Answer{Type: "noul", Noul: clamp(p)}
		}
	}
	return answers, nil
}

func (c *Client) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}
