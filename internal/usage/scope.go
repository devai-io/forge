package usage

import "context"

// Scope attributes provider calls without storing their prompts.
type Scope struct {
	Feature string
	Project string
	RunID   *int64
	ChatID  *int64
}
type scopeKey struct{}

func WithScope(ctx context.Context, s Scope) context.Context {
	return context.WithValue(ctx, scopeKey{}, s)
}
func ScopeOf(ctx context.Context) Scope { s, _ := ctx.Value(scopeKey{}).(Scope); return s }
