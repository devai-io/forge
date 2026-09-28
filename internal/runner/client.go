package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type client struct {
	base  string
	token string
	http  *http.Client
}

func newClient(base, token string) *client {
	// Longer than the server's 25 s claim long-poll.
	return &client{base: base, token: token, http: &http.Client{Timeout: 45 * time.Second}}
}

// post sends a JSON body and decodes a JSON answer into out (when non-nil
// and the server returned one). A 204 leaves out untouched and returns
// (false, nil).
func (c *client) post(ctx context.Context, path string, body, out any) (bool, error) {
	var buf bytes.Buffer
	if body == nil {
		body = struct{}{}
	}
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, &buf)
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "forge-agent/"+Version)
	resp, err := c.http.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return false, nil
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode >= 300 {
		return false, &apiError{status: resp.StatusCode, body: string(bytes.TrimSpace(raw))}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return false, fmt.Errorf("decoding %s: %w", path, err)
		}
	}
	return true, nil
}

type apiError struct {
	status int
	body   string
}

func (e *apiError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.status, e.body) }
