package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// The memory tools of `forge agent mcp`: recall, remember, correct, forget.

type mcpMemory struct {
	ID         int64   `json:"id"`
	ProjectKey *string `json:"project_key"`
	RepoName   *string `json:"repo_name"`
	Kind       string  `json:"kind"`
	Content    string  `json:"content"`
	Pinned     bool    `json:"pinned"`
}

func (m mcpMemory) line() string {
	scope := "user"
	if m.ProjectKey != nil {
		scope = *m.ProjectKey
	}
	if m.RepoName != nil {
		scope += "/" + *m.RepoName
	}
	pin := ""
	if m.Pinned {
		pin = ", pinned"
	}
	return fmt.Sprintf("- [m%d] (%s, %s%s) %s", m.ID, m.Kind, scope, pin, m.Content)
}

func argInt(a map[string]any, k string) int64 {
	switch v := a[k].(type) {
	case float64:
		return int64(v)
	case string:
		var n int64
		fmt.Sscan(strings.TrimPrefix(strings.TrimSpace(v), "m"), &n)
		return n
	}
	return 0
}

// send makes one request with a JSON body (nil: none) and decodes the answer.
func (c *client) send(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "forge-agent/"+Version)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode >= 300 {
		return &apiError{status: resp.StatusCode, body: strings.TrimSpace(string(data))}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

func memoryTool(ctx context.Context, c *client, name string, a map[string]any) (string, error) {
	cwd, _ := os.Getwd()
	switch name {
	case "forge_recall":
		q := url.Values{"cwd": {cwd}}
		for _, k := range []string{"query", "project"} {
			if v := argStr(a, k); v != "" {
				q.Set(map[string]string{"query": "q", "project": "project"}[k], v)
			}
		}
		if n := argInt(a, "limit"); n > 0 {
			q.Set("limit", fmt.Sprint(n))
		}
		var res struct {
			ProjectKey string      `json:"project_key"`
			Memories   []mcpMemory `json:"memories"`
		}
		if err := c.get(ctx, "/api/runner/memories?"+q.Encode(), &res); err != nil {
			return "", err
		}
		if len(res.Memories) == 0 {
			if res.ProjectKey == "" && argStr(a, "project") == "" {
				return "Nothing found. This directory belongs to no Forge project: pass project (forge_projects lists them).", nil
			}
			return "Nothing found.", nil
		}
		lines := make([]string, len(res.Memories))
		for i, m := range res.Memories {
			lines[i] = m.line()
		}
		return strings.Join(lines, "\n"), nil

	case "forge_remember":
		body := map[string]any{"content": argStr(a, "content"), "cwd": cwd}
		for k, dst := range map[string]string{"kind": "kind", "project": "project_key", "repo": "repo"} {
			if v := argStr(a, k); v != "" {
				body[dst] = v
			}
		}
		for _, k := range []string{"global", "pinned"} {
			if v, ok := a[k].(bool); ok {
				body[k] = v
			}
		}
		var res struct {
			Memory    mcpMemory   `json:"memory"`
			Duplicate bool        `json:"duplicate"`
			Similar   []mcpMemory `json:"similar"`
		}
		if err := c.send(ctx, http.MethodPost, "/api/runner/memories", body, &res); err != nil {
			return "", err
		}
		if res.Duplicate {
			return "Already in memory:\n" + res.Memory.line(), nil
		}
		out := "Saved:\n" + res.Memory.line()
		if len(res.Similar) > 0 {
			out += "\nSimilar memories (merge with forge_update_memory / forge_forget if they overlap):"
			for _, m := range res.Similar {
				out += "\n" + m.line()
			}
		}
		return out, nil

	case "forge_update_memory":
		id := argInt(a, "id")
		body := map[string]any{}
		for _, k := range []string{"content", "kind"} {
			if v := argStr(a, k); v != "" {
				body[k] = v
			}
		}
		if v, ok := a["pinned"].(bool); ok {
			body["pinned"] = v
		}
		if id <= 0 || len(body) == 0 {
			return "", fmt.Errorf("give the id and something to change")
		}
		var m mcpMemory
		if err := c.send(ctx, http.MethodPatch, fmt.Sprintf("/api/runner/memories/%d", id), body, &m); err != nil {
			return "", err
		}
		return "Updated:\n" + m.line(), nil

	case "forge_forget":
		id := argInt(a, "id")
		if id <= 0 {
			return "", fmt.Errorf("give the memory id")
		}
		if err := c.send(ctx, http.MethodDelete, fmt.Sprintf("/api/runner/memories/%d", id), nil, nil); err != nil {
			return "", err
		}
		return fmt.Sprintf("Forgot m%d.", id), nil
	}
	return "", fmt.Errorf("unknown tool %s", name)
}
