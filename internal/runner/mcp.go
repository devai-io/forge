package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// `forge agent mcp` is a Model Context Protocol server over stdio that Claude
// Code launches in every session on this machine (`claude mcp add --scope
// user forge -- forge agent mcp`). It speaks to Forge with this machine's
// runner token, so a Claude session can read its project's context and tasks
// and move them along without anyone copying anything between windows.
//
// `forge agent context --hook` is the SessionStart hook: it prints the Forge
// context for the session's directory (nothing, if the directory belongs to
// no project), so Claude starts every session already knowing where it is.

const mcpInstructions = `Forge is the user's command center: every project, its repos, servers, endpoints, tasks and the daily check-up.
When you start work in a repo, the SessionStart hook has usually already injected the Forge context; call forge_context if it is missing or stale.
Use forge_tasks / forge_task to see what is planned, forge_update_task to move a task (todo → in_progress → done) as you actually work on it, forge_comment to leave a short note of what you did (commit hashes, decisions), and forge_create_task for follow-ups you discover. Refer to tasks by ref, e.g. ALPHA-12.
Respect each project's change-control notes (e.g. "production changes need explicit approval") — Forge context includes them.`

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

// readOnlyTools only read Forge. Their readOnlyHint lets Claude Code use them
// in plan mode (a read-only Assistant chat, a plan run); the others change
// tasks and are left to modes that allow changes.
var readOnlyTools = map[string]bool{"forge_context": true, "forge_projects": true, "forge_tasks": true,
	"forge_task": true, "forge_checkup": true}

func listedTools() []map[string]any {
	out := make([]map[string]any, len(mcpTools))
	for i, t := range mcpTools {
		out[i] = map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.InputSchema,
			"annotations": map[string]any{"readOnlyHint": readOnlyTools[t.Name], "destructiveHint": false,
				"openWorldHint": false}}
	}
	return out
}

var taskStatus = map[string]any{"type": "string", "enum": []string{"backlog", "todo", "in_progress", "blocked", "done"}}
var taskPriority = map[string]any{"type": "string", "enum": []string{"urgent", "high", "medium", "low"}}

var mcpTools = []tool{
	{"forge_context", "The Forge context for a directory: project, repo state, infra & change-control notes, open tasks, open check-up actions. Defaults to the session's working directory.",
		obj(map[string]any{"cwd": str("absolute directory; defaults to the current one")})},
	{"forge_projects", "List every Forge project with key, status, priority and open task count.", obj(map[string]any{})},
	{"forge_tasks", "List tasks, optionally for one project (key like ALPHA) or matching a search; open tasks only unless include_done.",
		obj(map[string]any{"project": str("project key"), "query": str("search in title, ref, description"),
			"include_done": map[string]any{"type": "boolean"}})},
	{"forge_task", "Show one task in full (description included) by ref, e.g. ALPHA-12.", obj(map[string]any{"task": str("task ref")}, "task")},
	{"forge_create_task", "Create a task in a project.",
		obj(map[string]any{"project": str("project key"), "title": str("imperative, specific"), "description": str("markdown"),
			"priority": taskPriority, "status": taskStatus,
			"type":     map[string]any{"type": "string", "enum": []string{"feature", "bug", "chore", "research", "ops"}},
			"labels":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"due_date": str("YYYY-MM-DD")}, "project", "title")},
	{"forge_update_task", "Update a task: status, title, description, priority, focus, labels, due date.",
		obj(map[string]any{"task": str("task ref, e.g. ALPHA-12"), "status": taskStatus, "title": str(""),
			"description": str("markdown (replaces)"), "priority": taskPriority, "focus": map[string]any{"type": "boolean"},
			"labels": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "due_date": str("YYYY-MM-DD, or empty to clear")}, "task")},
	{"forge_comment", "Add a markdown comment to a task (what was done, commit hashes, decisions).",
		obj(map[string]any{"task": str("task ref"), "body": str("markdown")}, "task", "body")},
	{"forge_checkup", "Today's check-up action list (failing and warning items), optionally for one project.",
		obj(map[string]any{"project": str("project key")})},
}

// ServeMCP runs the stdio MCP loop until stdin closes.
func ServeMCP(cfg *Config, in io.Reader, out io.Writer) error {
	c := newClient(cfg.APIURL, cfg.Token)
	enc := json.NewEncoder(out)
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var m rpcMsg
		if json.Unmarshal(sc.Bytes(), &m) != nil || m.Method == "" {
			continue
		}
		if len(m.ID) == 0 {
			continue // a notification (initialized, cancelled): nothing to answer
		}
		result, rpcErr := handleMCP(c, m)
		resp := map[string]any{"jsonrpc": "2.0", "id": m.ID}
		if rpcErr != nil {
			resp["error"] = rpcErr
		} else {
			resp["result"] = result
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return sc.Err()
}

func handleMCP(c *client, m rpcMsg) (any, map[string]any) {
	switch m.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(m.Params, &p)
		version := p.ProtocolVersion
		if version == "" {
			version = "2025-06-18"
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "forge", "version": Version},
			"instructions":    mcpInstructions,
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": listedTools()}, nil
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return nil, map[string]any{"code": -32602, "message": "invalid params"}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		text, err := callTool(ctx, c, p.Name, p.Arguments)
		if err != nil {
			return map[string]any{"content": []map[string]any{{"type": "text", "text": "Error: " + err.Error()}}, "isError": true}, nil
		}
		return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}, nil
	}
	return nil, map[string]any{"code": -32601, "message": "method not found: " + m.Method}
}

func argStr(a map[string]any, k string) string {
	if v, ok := a[k].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func (c *client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "forge-agent/"+Version)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode >= 300 {
		return &apiError{status: resp.StatusCode, body: strings.TrimSpace(string(raw))}
	}
	return json.Unmarshal(raw, out)
}

func (c *client) patch(ctx context.Context, path string, body, out any) error {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, c.base+path, strings.NewReader(string(raw)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode >= 300 {
		return &apiError{status: resp.StatusCode, body: strings.TrimSpace(string(data))}
	}
	return json.Unmarshal(data, out)
}

type mcpTask struct {
	ID          int64    `json:"id"`
	Ref         string   `json:"ref"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Priority    string   `json:"priority"`
	Type        string   `json:"type"`
	Labels      []string `json:"labels"`
	DueDate     *string  `json:"due_date"`
	Focus       bool     `json:"focus"`
	RepoName    *string  `json:"repo_name"`
	ProjectKey  string   `json:"project_key"`
}

func (t mcpTask) line() string {
	extra := ""
	if t.Focus {
		extra += " ★focus"
	}
	if t.DueDate != nil {
		extra += " due " + *t.DueDate
	}
	if t.RepoName != nil {
		extra += " [" + *t.RepoName + "]"
	}
	if len(t.Labels) > 0 {
		extra += " #" + strings.Join(t.Labels, " #")
	}
	return fmt.Sprintf("- %s (%s, %s, %s): %s%s", t.Ref, t.Status, t.Priority, t.Type, t.Title, extra)
}

func callTool(ctx context.Context, c *client, name string, a map[string]any) (string, error) {
	switch name {
	case "forge_context":
		cwd := argStr(a, "cwd")
		if cwd == "" {
			cwd, _ = os.Getwd()
		}
		var res struct {
			Markdown string `json:"markdown"`
		}
		if err := c.get(ctx, "/api/runner/context?cwd="+url.QueryEscape(cwd), &res); err != nil {
			var ae *apiError
			if asAPIError(err, &ae) && ae.status == 404 {
				return cwd + " is not inside any Forge project's repos. forge_projects lists them.", nil
			}
			return "", err
		}
		return res.Markdown, nil

	case "forge_projects":
		var res struct {
			Projects []struct {
				Key, Name, Status, Summary string
				Priority, Open             int
			} `json:"projects"`
		}
		if err := c.get(ctx, "/api/runner/projects", &res); err != nil {
			return "", err
		}
		var b strings.Builder
		for _, p := range res.Projects {
			fmt.Fprintf(&b, "- %s %s — %s, P%d, %d open — %s\n", p.Key, p.Name, p.Status, p.Priority, p.Open, p.Summary)
		}
		return b.String(), nil

	case "forge_tasks", "forge_task":
		q := url.Values{}
		if p := argStr(a, "project"); p != "" {
			q.Set("project", p)
		}
		if s := argStr(a, "query"); s != "" {
			q.Set("q", s)
		}
		if name == "forge_task" {
			q.Set("q", argStr(a, "task"))
		} else if v, _ := a["include_done"].(bool); !v {
			q.Set("open", "1")
		}
		var res struct {
			Tasks []mcpTask `json:"tasks"`
		}
		if err := c.get(ctx, "/api/runner/tasks?"+q.Encode(), &res); err != nil {
			return "", err
		}
		if name == "forge_task" {
			ref := strings.ToUpper(argStr(a, "task"))
			for _, t := range res.Tasks {
				if t.Ref == ref {
					return fmt.Sprintf("%s\n\n%s", strings.TrimPrefix(t.line(), "- "), t.Description), nil
				}
			}
			return "", fmt.Errorf("no task %s", ref)
		}
		if len(res.Tasks) == 0 {
			return "No matching tasks.", nil
		}
		lines := make([]string, 0, len(res.Tasks))
		for _, t := range res.Tasks {
			lines = append(lines, t.line())
		}
		return strings.Join(lines, "\n"), nil

	case "forge_create_task":
		body := map[string]any{"project_key": argStr(a, "project"), "title": argStr(a, "title")}
		for _, k := range []string{"description", "priority", "status", "type", "due_date"} {
			if v := argStr(a, k); v != "" {
				body[k] = v
			}
		}
		if l, ok := a["labels"]; ok {
			body["labels"] = l
		}
		var t mcpTask
		if _, err := c.post(ctx, "/api/runner/tasks", body, &t); err != nil {
			return "", err
		}
		return "Created " + strings.TrimPrefix(t.line(), "- "), nil

	case "forge_update_task":
		ref := argStr(a, "task")
		body := map[string]any{}
		for _, k := range []string{"status", "title", "description", "priority"} {
			if v := argStr(a, k); v != "" {
				body[k] = v
			}
		}
		if v, ok := a["focus"].(bool); ok {
			body["focus"] = v
		}
		if l, ok := a["labels"]; ok {
			body["labels"] = l
		}
		if d, ok := a["due_date"].(string); ok {
			if strings.TrimSpace(d) == "" {
				body["due_date"] = nil
			} else {
				body["due_date"] = d
			}
		}
		if len(body) == 0 {
			return "", fmt.Errorf("nothing to change")
		}
		var t mcpTask
		if err := c.patch(ctx, "/api/runner/tasks/"+url.PathEscape(ref), body, &t); err != nil {
			return "", err
		}
		return "Updated " + strings.TrimPrefix(t.line(), "- "), nil

	case "forge_comment":
		var out map[string]any
		if _, err := c.post(ctx, "/api/runner/tasks/"+url.PathEscape(argStr(a, "task"))+"/comments",
			map[string]string{"body": argStr(a, "body")}, &out); err != nil {
			return "", err
		}
		return "Comment added to " + strings.ToUpper(argStr(a, "task")) + ".", nil

	case "forge_checkup":
		var cu struct {
			Date   string `json:"date"`
			Status string `json:"status"`
			Items  []struct {
				Severity, Title, Action string
				Done                    bool
				ProjectKey              *string `json:"project_key"`
				Category                string
			} `json:"items"`
		}
		if err := c.get(ctx, "/api/runner/checkup", &cu); err != nil {
			return "", err
		}
		project := strings.ToUpper(argStr(a, "project"))
		var b strings.Builder
		fmt.Fprintf(&b, "Check-up %s — %s\n", cu.Date, cu.Status)
		n := 0
		for _, it := range cu.Items {
			if it.Severity == "ok" || it.Done || (project != "" && (it.ProjectKey == nil || *it.ProjectKey != project)) {
				continue
			}
			n++
			fmt.Fprintf(&b, "- [%s/%s] %s → %s\n", it.Severity, it.Category, it.Title, it.Action)
		}
		if n == 0 {
			b.WriteString("Nothing open.\n")
		}
		return b.String(), nil
	}
	return "", fmt.Errorf("unknown tool %s", name)
}

func asAPIError(err error, target **apiError) bool {
	ae, ok := err.(*apiError)
	if ok {
		*target = ae
	}
	return ok
}

// PrintContext is `forge agent context`: the Forge context for a directory.
// As a SessionStart hook (--hook) it reads the hook's JSON on stdin and emits
// additionalContext; any failure prints nothing, so a hook can never block
// or break a session.
func PrintContext(cfg *Config, hook bool, in io.Reader, out io.Writer) {
	cwd, _ := os.Getwd()
	if hook {
		var h struct {
			Cwd    string `json:"cwd"`
			Source string `json:"source"`
		}
		raw, _ := io.ReadAll(io.LimitReader(in, 1<<20))
		if json.Unmarshal(raw, &h) == nil && h.Cwd != "" {
			cwd = h.Cwd
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	c := newClient(cfg.APIURL, cfg.Token)
	var res struct {
		Markdown string `json:"markdown"`
	}
	if err := c.get(ctx, "/api/runner/context?cwd="+url.QueryEscape(cwd), &res); err != nil || res.Markdown == "" {
		return
	}
	if !hook {
		fmt.Fprint(out, res.Markdown)
		return
	}
	_ = json.NewEncoder(out).Encode(map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName": "SessionStart", "additionalContext": res.Markdown}})
}
