// Package jev talks to TypeSafe's Jev: a small, fast model that answers
// typed questions (yes/no as a probability, or one of a set of choices)
// instead of writing text. Forge uses it where a routine judgement would
// otherwise cost frontier-model tokens: which model a run needs, which open
// tasks a Claude session should be told about. Billing is per input token,
// so callers keep the state they send short.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/devai-io/forge/internal/usage"
)

// URL is TypeSafe's System One endpoint.
const URL = "https://api.typesafe.ai/v1/systemone"

// Model is the Jev model asked; "jev-latest" follows TypeSafe's releases.
const Model = "jev-latest"

// ErrNoKey means no API key is configured (the vault item tagged
// integration:jev).
var ErrNoKey = errors.New("no Jev API key configured")

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

type Client struct {
	// Key returns the API key (read from the vault on each call, so a new
	// key applies without a restart).
	Key  func(context.Context) (string, error)
	URL  string
	HTTP *http.Client

	mu    sync.Mutex
	stats Stats
}

func New(key func(context.Context) (string, error)) *Client {
	return &Client{Key: key, URL: URL, HTTP: &http.Client{Timeout: 10 * time.Second}}
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

func (c *Client) ask(ctx context.Context, state any, questions map[string]Question) (map[string]Answer, error) {
	key, err := c.Key(ctx)
	if err != nil || key == "" {
		return nil, ErrNoKey
	}
	body, err := json.Marshal(map[string]any{"model": Model, "state": state, "questions": questions})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jev: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		msg := string(raw)
		if len(msg) > 200 {
			msg = msg[:200]
		}
		if resp.StatusCode == http.StatusPaymentRequired {
			msg = "prepaid balance is empty"
		}
		return nil, fmt.Errorf("jev: HTTP %d: %s", resp.StatusCode, msg)
	}
	var out struct {
		Model   string            `json:"model"`
		Answers map[string]Answer `json:"answers"`
		Usage   struct {
			InputTokens int `json:"input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Answers == nil {
		return nil, errors.New("jev: malformed response")
	}
	c.mu.Lock() // billed, whether or not the answers are usable
	c.stats.InputTokens += out.Usage.InputTokens
	c.stats.LastModel = out.Model
	c.mu.Unlock()
	usage.From(ctx).Add(usage.Entry{API: "jev", Model: out.Model, Calls: 1, InputTokens: int64(out.Usage.InputTokens),
		CostUSD: usage.JevCost(int64(out.Usage.InputTokens))})
	for name := range questions {
		if _, ok := out.Answers[name]; !ok {
			return nil, fmt.Errorf("jev: no answer for %q", name)
		}
	}
	return out.Answers, nil
}

func (c *Client) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}
