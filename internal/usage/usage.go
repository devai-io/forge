// Package usage records what a piece of work spent, per API and model: an
// Assistant turn carries a Meter in its context, and every client that
// calls a paid API on its behalf (the chat model, Jev) adds to it. It also
// holds the DeepSeek price list Forge estimates costs with.
package usage

import (
	"context"
	"strings"
	"sync"
	"time"
)

// Entry is one API/model line of a bill.
type Entry struct {
	API          string   `json:"api"`   // "deepseek" | "jev" | "claude-code" | an API host
	Model        string   `json:"model"` // as the API named it
	Calls        int      `json:"calls"`
	InputTokens  int64    `json:"input_tokens"` // not read from cache
	CachedTokens int64    `json:"cached_tokens"`
	OutputTokens int64    `json:"output_tokens"`
	CostUSD      *float64 `json:"cost_usd"` // nil = unknown (no public price)
	Note         string   `json:"note,omitempty"`
}

// Meter adds up entries by API and model. Safe for concurrent use; a nil
// Meter ignores everything.
type Meter struct {
	mu      sync.Mutex
	entries map[string]*Entry
	order   []string
}

func NewMeter() *Meter { return &Meter{entries: map[string]*Entry{}} }

func (m *Meter) Add(e Entry) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := e.API + "\x00" + e.Model
	cur, ok := m.entries[k]
	if !ok {
		c := e
		if e.CostUSD != nil {
			v := *e.CostUSD
			c.CostUSD = &v
		}
		m.entries[k] = &c
		m.order = append(m.order, k)
		return
	}
	cur.Calls += e.Calls
	cur.InputTokens += e.InputTokens
	cur.CachedTokens += e.CachedTokens
	cur.OutputTokens += e.OutputTokens
	if e.CostUSD != nil {
		if cur.CostUSD == nil {
			cur.CostUSD = new(float64)
		}
		*cur.CostUSD += *e.CostUSD
	}
	if e.Note != "" {
		cur.Note = e.Note
	}
}

// Entries in the order they first appeared.
func (m *Meter) Entries() []Entry {
	if m == nil {
		return []Entry{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Entry, 0, len(m.order))
	for _, k := range m.order {
		out = append(out, *m.entries[k])
	}
	return out
}

type ctxKey struct{}

func WithMeter(ctx context.Context, m *Meter) context.Context {
	return context.WithValue(ctx, ctxKey{}, m)
}

// From is the context's meter, or nil.
func From(ctx context.Context) *Meter {
	m, _ := ctx.Value(ctxKey{}).(*Meter)
	return m
}

// Total of the priced entries, and whether any entry had no price.
func Total(es []Entry) (total float64, unpriced bool) {
	for _, e := range es {
		if e.CostUSD == nil {
			unpriced = unpriced || e.Calls > 0
			continue
		}
		total += *e.CostUSD
	}
	return total, unpriced
}

// ── DeepSeek prices ───────────────────────────────────────────────────────

// DeepSeek's list prices, USD per million tokens, off-peak; peak hours
// (01-04 and 06-10 UTC on weekdays) cost double.
type price struct{ hit, miss, out float64 }

var deepseekPrices = map[string]price{
	"flash": {0.003, 0.15, 0.6},
	"pro":   {0.022, 0.66, 1.98},
}

// DeepSeekPeak says whether at falls in DeepSeek's double-price hours.
func DeepSeekPeak(at time.Time) bool {
	at = at.UTC()
	if at.Weekday() == time.Saturday || at.Weekday() == time.Sunday {
		return false
	}
	h := at.Hour()
	return (h >= 1 && h < 4) || (h >= 6 && h < 10)
}

// DeepSeekCost prices one call's tokens (cached = cache hits, input =
// misses) for a DeepSeek model at the time it ran; nil for a model this
// list does not know.
func DeepSeekCost(model string, input, cached, output int64, at time.Time) *float64 {
	m := strings.ToLower(model)
	var p price
	switch {
	case !strings.HasPrefix(m, "deepseek"):
		return nil
	case strings.Contains(m, "pro"):
		p = deepseekPrices["pro"]
	default:
		p = deepseekPrices["flash"]
	}
	c := (float64(cached)*p.hit + float64(input)*p.miss + float64(output)*p.out) / 1e6
	if DeepSeekPeak(at) {
		c *= 2
	}
	return &c
}

// JevPerMillion is Jev's input price at its smallest prepaid packs (larger
// packs cost less; output is free), so Jev costs are upper estimates.
const JevPerMillion = 0.42

// JevCost prices a Jev call's input tokens.
func JevCost(input int64) *float64 {
	c := float64(input) * JevPerMillion / 1e6
	return &c
}
