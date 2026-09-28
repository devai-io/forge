package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/devai-io/forge/internal/llm"
	"github.com/devai-io/forge/internal/store"
)

// The assistant's tools. Each returns a JSON-able result (what the model
// reads), a one-line summary (what the chat shows) or an error (shown to
// both). Results are kept small: every byte is paid for again on each step.

type object = map[string]any

func prop(typ, desc string) object { return object{"type": typ, "description": desc} }

func params(required []string, props object) object {
	if required == nil {
		required = []string{}
	}
	return object{"type": "object", "properties": props, "required": required}
}

func toolDefs() []llm.Tool {
	return []llm.Tool{
		{Name: "list_projects", Description: "All active projects with their key, status, priority, open task count and repos (name, kind).",
			Parameters: params(nil, object{})},
		{Name: "get_project", Description: "One project in detail: description, infra notes, repos with git/CI state, servers, endpoints that are down.",
			Parameters: params([]string{"project"}, object{"project": prop("string", "Project key, e.g. SHOP")})},
		{Name: "list_tasks", Description: "Open tasks, optionally for one project or matching a search. Most important first.",
			Parameters: params(nil, object{
				"project": prop("string", "Project key (optional)"),
				"query":   prop("string", "Search in titles and descriptions (optional)"),
				"status":  prop("string", "backlog | todo | in_progress | blocked | done (optional; default all open)"),
				"limit":   prop("integer", "At most this many (default 30, max 100)"),
			})},
		{Name: "get_task", Description: "A task with its description, comments and runs.",
			Parameters: params([]string{"ref"}, object{"ref": prop("string", "Task ref, e.g. SHOP-12")})},
		{Name: "create_task", Description: "Create a task on the board.",
			Parameters: params([]string{"project", "title"}, object{
				"project":     prop("string", "Project key"),
				"title":       prop("string", "Short title"),
				"description": prop("string", "Markdown details (optional)"),
				"priority":    prop("string", "urgent | high | medium | low (default medium)"),
				"type":        prop("string", "feature | bug | chore | research | ops (default feature)"),
				"status":      prop("string", "backlog | todo | in_progress (default todo)"),
				"repo":        prop("string", "Repo name the task is about (optional)"),
			})},
		{Name: "update_task", Description: "Change a task's status, priority, title or description.",
			Parameters: params([]string{"ref"}, object{
				"ref":         prop("string", "Task ref"),
				"status":      prop("string", "backlog | todo | in_progress | blocked | done"),
				"priority":    prop("string", "urgent | high | medium | low"),
				"title":       prop("string", "New title"),
				"description": prop("string", "New description (replaces the old one)"),
			})},
		{Name: "comment_on_task", Description: "Add a comment to a task (e.g. what a run found or changed).",
			Parameters: params([]string{"ref", "body"}, object{"ref": prop("string", "Task ref"), "body": prop("string", "Markdown")})},
		{Name: "list_machines", Description: "The machines running the Forge agent: role (master/ios/worker), online, whether Claude Code is there, allowed permission modes, named commands.",
			Parameters: params(nil, object{})},
		{Name: "get_overview", Description: "Today at a glance: task stats, focus and overdue tasks, endpoints down, active runs, open check-up actions.",
			Parameters: params(nil, object{})},
		{Name: "list_runs", Description: "Recent runs (Claude Code sessions and commands), newest first.",
			Parameters: params(nil, object{
				"project": prop("string", "Project key (optional)"),
				"active":  prop("boolean", "Only queued/running ones"),
				"limit":   prop("integer", "Default 10, max 50"),
			})},
		{Name: "delegate_to_claude", Description: "Queue a Claude Code session in a repo on one of the machines. Returns the run id; use wait_for_runs or get_run to follow it.",
			Parameters: params([]string{"project", "repo", "prompt"}, object{
				"project":         prop("string", "Project key"),
				"repo":            prop("string", "Repo name within the project"),
				"prompt":          prop("string", "Complete, self-contained instructions for Claude Code"),
				"machine":         prop("string", "Machine name (optional; default the master)"),
				"permission_mode": prop("string", "plan (read-only, default) | acceptEdits (may change files)"),
				"model":           prop("string", "Claude model alias, e.g. sonnet or opus (optional; default chosen by Forge)"),
				"worktree":        prop("boolean", "Work in an isolated git worktree/branch (recommended for changes)"),
				"task_ref":        prop("string", "Link the run to this task (optional)"),
			})},
		{Name: "run_command", Description: "Run one of a machine's named commands (see list_machines) in a repo. Commands that need confirmation are refused.",
			Parameters: params([]string{"project", "repo", "command"}, object{
				"project": prop("string", "Project key"),
				"repo":    prop("string", "Repo name"),
				"command": prop("string", "Command name"),
				"machine": prop("string", "Machine name (optional; default the master)"),
			})},
		{Name: "get_run", Description: "A run's status and, when finished, its result (Claude's final answer or the command's output tail).",
			Parameters: params([]string{"run_id"}, object{"run_id": prop("integer", "Run id")})},
		{Name: "wait_for_runs", Description: "Wait until the runs finish (or max_seconds pass) and return their status and results.",
			Parameters: params([]string{"run_ids"}, object{
				"run_ids":     object{"type": "array", "items": object{"type": "integer"}, "description": "Run ids"},
				"max_seconds": prop("integer", "Give up after this long (default 300, max 600)"),
			})},
	}
}

// call runs one tool.
func (a *Assistant) call(ctx context.Context, user *store.User, name string, raw json.RawMessage) (any, string, error) {
	var in struct {
		Project, Repo, Prompt, Machine, PermissionMode, Model, TaskRef, Ref, Query, Status, Title string
		Description, Priority, Type, Body, Command                                                string
		Worktree, Active                                                                          bool
		Limit, RunID, MaxSeconds                                                                  int
		RunIDs                                                                                    []int64
	}
	var loose map[string]any
	if err := json.Unmarshal(raw, &loose); err != nil {
		return nil, "", fmt.Errorf("arguments are not a JSON object: %w", err)
	}
	// Tool arguments use snake_case; map them onto the struct by hand so a
	// model sending a number as a string still works.
	str := func(k string) string {
		switch v := loose[k].(type) {
		case string:
			return strings.TrimSpace(v)
		case float64:
			return fmt.Sprint(int64(v))
		}
		return ""
	}
	num := func(k string) int {
		switch v := loose[k].(type) {
		case float64:
			return int(v)
		case string:
			var n int
			fmt.Sscan(v, &n)
			return n
		}
		return 0
	}
	in.Project, in.Repo, in.Prompt, in.Machine = strings.ToUpper(str("project")), str("repo"), str("prompt"), str("machine")
	in.PermissionMode, in.Model, in.TaskRef, in.Ref = str("permission_mode"), str("model"), strings.ToUpper(str("task_ref")), strings.ToUpper(str("ref"))
	in.Query, in.Status, in.Title, in.Description = str("query"), str("status"), str("title"), str("description")
	in.Priority, in.Type, in.Body, in.Command = str("priority"), str("type"), str("body"), str("command")
	in.Worktree, _ = loose["worktree"].(bool)
	in.Active, _ = loose["active"].(bool)
	in.Limit, in.RunID, in.MaxSeconds = num("limit"), num("run_id"), num("max_seconds")
	if ids, ok := loose["run_ids"].([]any); ok {
		for _, v := range ids {
			if f, ok := v.(float64); ok {
				in.RunIDs = append(in.RunIDs, int64(f))
			}
		}
	}
	today := store.TodayFor(user.Timezone)

	switch name {
	case "list_projects":
		projects, err := a.store.ListProjects(ctx, today, false)
		if err != nil {
			return nil, "", err
		}
		repos, err := a.store.RunnerRepos(ctx)
		if err != nil {
			return nil, "", err
		}
		byKey := map[string][]string{}
		for _, r := range repos {
			byKey[r.ProjectKey] = append(byKey[r.ProjectKey], r.Name)
		}
		out := []object{}
		for _, p := range projects {
			out = append(out, object{"key": p.Key, "name": p.Name, "status": p.Status, "priority": p.Priority,
				"summary": p.Summary, "open_tasks": p.Stats.Total - p.Stats.Done, "repos": byKey[p.Key]})
		}
		return object{"projects": out}, fmt.Sprintf("Listed %d projects", len(out)), nil

	case "get_project":
		p, err := a.store.ProjectByKey(ctx, today, in.Project)
		if err != nil {
			return nil, "", fmt.Errorf("no project %q", in.Project)
		}
		repos := []object{}
		for _, r := range p.Repos {
			o := object{"name": r.Name, "kind": r.Kind, "has_path": r.Path != "", "deploy": r.Deploy}
			if g := r.Git; g != nil {
				o["git"] = object{"branch": g.Branch, "changed": g.Dirty, "ahead": g.Ahead, "behind": g.Behind,
					"last_commit": g.Head}
				if g.CI != nil {
					o["ci"] = g.CI.Conclusion
				}
			}
			repos = append(repos, o)
		}
		down := []string{}
		for _, e := range p.Endpoints {
			if e.LastStatus == "down" {
				down = append(down, e.Name+" "+e.URL)
			}
		}
		servers := []string{}
		for _, s := range p.Servers {
			servers = append(servers, s.Name+" ("+s.Role+")")
		}
		return object{"key": p.Key, "name": p.Name, "status": p.Status, "summary": p.Summary,
				"description": clip(p.Description, 3000), "infra_notes": clip(p.InfraNotes, 3000), "repos": repos,
				"servers": servers, "endpoints_down": down, "stats": p.Stats},
			"Looked at " + p.Key, nil

	case "list_tasks":
		limit := in.Limit
		if limit <= 0 || limit > 100 {
			limit = 30
		}
		f := store.TaskFilter{ProjectKey: in.Project, Query: in.Query, Limit: limit, Today: today}
		if in.Status != "" {
			f.Statuses = []string{in.Status}
		} else {
			f.Open = true
		}
		tasks, err := a.store.ListTasks(ctx, f)
		if err != nil {
			return nil, "", err
		}
		out := []object{}
		for _, t := range tasks {
			o := object{"ref": t.Ref, "title": t.Title, "status": t.Status, "priority": t.Priority, "type": t.Type}
			if t.RepoName != nil {
				o["repo"] = *t.RepoName
			}
			if t.DueDate != nil {
				o["due"] = *t.DueDate
			}
			if t.Focus {
				o["focus"] = true
			}
			out = append(out, o)
		}
		return object{"tasks": out}, fmt.Sprintf("Listed %d tasks", len(out)), nil

	case "get_task":
		t, err := a.store.TaskByRef(ctx, in.Ref)
		if err != nil {
			return nil, "", fmt.Errorf("no task %q", in.Ref)
		}
		d, err := a.store.TaskDetail(ctx, t.ID)
		if err != nil {
			return nil, "", err
		}
		comments := []string{}
		for _, c := range d.Comments {
			comments = append(comments, c.CreatedAt.Format("2006-01-02")+": "+clip(c.Body, 800))
		}
		runs := []object{}
		for _, r := range d.Runs {
			runs = append(runs, object{"id": r.ID, "status": r.Status, "kind": r.Kind})
		}
		return object{"ref": t.Ref, "id": t.ID, "title": t.Title, "status": t.Status, "priority": t.Priority,
			"type": t.Type, "description": clip(t.Description, 4000), "labels": t.Labels, "repo": t.RepoName,
			"due": t.DueDate, "comments": comments, "runs": runs}, "Read " + t.Ref, nil

	case "create_task":
		ti := store.TaskInput{ProjectKey: in.Project, Title: in.Title, Description: in.Description,
			Priority: in.Priority, Type: in.Type, Status: in.Status}
		if in.Repo != "" {
			if r, err := a.repo(ctx, today, in.Project, in.Repo); err == nil {
				ti.RepoID = &r.ID
			}
		}
		t, err := a.store.CreateTask(ctx, ti)
		if err != nil {
			return nil, "", err
		}
		return object{"task": t}, "Created " + t.Ref + ": " + t.Title, nil

	case "update_task":
		t, err := a.store.TaskByRef(ctx, in.Ref)
		if err != nil {
			return nil, "", fmt.Errorf("no task %q", in.Ref)
		}
		p := store.Patch{}
		for k, v := range map[string]string{"status": in.Status, "priority": in.Priority, "title": in.Title, "description": in.Description} {
			if v != "" {
				p[k], _ = json.Marshal(v)
			}
		}
		if len(p) == 0 {
			return nil, "", errors.New("nothing to change")
		}
		nt, err := a.store.UpdateTask(ctx, t.ID, p)
		if err != nil {
			return nil, "", err
		}
		return object{"task": nt}, "Updated " + nt.Ref, nil

	case "comment_on_task":
		t, err := a.store.TaskByRef(ctx, in.Ref)
		if err != nil {
			return nil, "", fmt.Errorf("no task %q", in.Ref)
		}
		c, err := a.store.CreateComment(ctx, t.ID, in.Body)
		if err != nil {
			return nil, "", err
		}
		return object{"comment_id": c.ID, "task": t.Ref}, "Commented on " + t.Ref, nil

	case "list_machines":
		runners, err := a.store.ListRunners(ctx)
		if err != nil {
			return nil, "", err
		}
		out := []object{}
		for _, r := range runners {
			cmds := []object{}
			for _, c := range r.Capabilities.CommandDetails {
				cmds = append(cmds, object{"name": c.Name, "description": c.Description, "repos": c.Repos, "needs_confirmation": c.Confirm})
			}
			out = append(out, object{"name": r.Name, "role": r.Role, "online": r.Online, "os": r.OS,
				"claude": r.Capabilities.Claude, "permission_modes": r.Capabilities.PermissionModes, "commands": cmds,
				"running": r.Running})
		}
		return object{"machines": out}, fmt.Sprintf("Listed %d machines", len(out)), nil

	case "get_overview":
		d, err := a.store.Dashboard(ctx, user.Timezone)
		if err != nil {
			return nil, "", err
		}
		refs := func(ts []store.Task) []string {
			out := []string{}
			for _, t := range ts {
				out = append(out, t.Ref+" "+t.Title)
			}
			return out
		}
		down := []string{}
		for _, e := range d.EndpointsDown {
			down = append(down, e.ProjectKey+": "+e.Name)
		}
		active := []object{}
		for _, r := range d.ActiveRuns {
			active = append(active, object{"id": r.ID, "status": r.Status, "repo": r.RepoName, "machine": r.RunnerName})
		}
		out := object{"today": d.Today, "stats": d.Stats, "focus": refs(d.Focus), "overdue": refs(d.Overdue),
			"endpoints_down": down, "active_runs": active}
		if c, err := a.store.LatestCheckup(ctx); err == nil {
			actions := []string{}
			for _, it := range c.Items {
				if it.Severity != "ok" && !it.Done {
					actions = append(actions, it.Severity+": "+it.Title)
				}
			}
			out["checkup"] = object{"date": c.Date, "status": c.Status, "open_actions": actions}
		}
		return out, "Checked today's overview", nil

	case "list_runs":
		limit := in.Limit
		if limit <= 0 || limit > 50 {
			limit = 10
		}
		f := store.RunFilter{ProjectKey: in.Project, Limit: limit}
		if in.Active {
			f.Statuses = []string{"queued", "running"}
		}
		runs, err := a.store.ListRuns(ctx, f)
		if err != nil {
			return nil, "", err
		}
		out := []object{}
		for _, r := range runs {
			out = append(out, runSummary(&r, false))
		}
		return object{"runs": out}, fmt.Sprintf("Listed %d runs", len(out)), nil

	case "delegate_to_claude":
		if in.Prompt == "" {
			return nil, "", errors.New("prompt is required")
		}
		mode := in.PermissionMode
		switch mode {
		case "":
			mode = "plan"
		case "plan", "acceptEdits":
		default:
			return nil, "", fmt.Errorf("permission_mode must be plan or acceptEdits, not %q", mode)
		}
		repo, err := a.repo(ctx, today, in.Project, in.Repo)
		if err != nil {
			return nil, "", err
		}
		rn, err := a.machine(ctx, in.Machine, "")
		if err != nil {
			return nil, "", err
		}
		ri := store.RunInput{RunnerID: rn.ID, RepoID: repo.ID, Kind: "agent", Prompt: in.Prompt,
			PermissionMode: mode, Model: in.Model, Worktree: in.Worktree}
		if in.TaskRef != "" {
			t, err := a.store.TaskByRef(ctx, in.TaskRef)
			if err != nil {
				return nil, "", fmt.Errorf("no task %q", in.TaskRef)
			}
			ri.TaskID = &t.ID
		}
		if a.hooks.Route != nil {
			a.hooks.Route(ctx, &ri)
		}
		run, err := a.store.CreateRun(ctx, ri)
		if err != nil {
			return nil, "", err
		}
		if a.hooks.Queued != nil {
			a.hooks.Queued(run.RunnerID)
		}
		out := object{"run_id": run.ID, "machine": run.RunnerName, "project": run.ProjectKey, "repo": run.RepoName,
			"permission_mode": run.PermissionMode, "model": run.Model, "model_note": run.ModelNote, "status": run.Status}
		if !rn.Online {
			out["note"] = rn.Name + " is offline; the run waits until it reconnects"
		}
		return out, fmt.Sprintf("Queued Claude Code run #%d on %s (%s/%s, %s)", run.ID, run.RunnerName, run.ProjectKey, run.RepoName, mode), nil

	case "run_command":
		repo, err := a.repo(ctx, today, in.Project, in.Repo)
		if err != nil {
			return nil, "", err
		}
		rn, err := a.machine(ctx, in.Machine, in.Command)
		if err != nil {
			return nil, "", err
		}
		if d, ok := rn.Capabilities.Command(in.Command); ok && d.Confirm {
			return nil, "", fmt.Errorf("%q needs the user's confirmation: they can run it from the Agents page", in.Command)
		}
		run, err := a.store.CreateRun(ctx, store.RunInput{RunnerID: rn.ID, RepoID: repo.ID, Kind: "command", Command: in.Command})
		if err != nil {
			return nil, "", err
		}
		if a.hooks.Queued != nil {
			a.hooks.Queued(run.RunnerID)
		}
		return object{"run_id": run.ID, "machine": run.RunnerName, "project": run.ProjectKey, "repo": run.RepoName,
				"command": run.Command, "status": run.Status},
			fmt.Sprintf("Queued %s as run #%d on %s", in.Command, run.ID, run.RunnerName), nil

	case "get_run":
		r, err := a.store.RunByID(ctx, int64(in.RunID))
		if err != nil {
			return nil, "", fmt.Errorf("no run %d", in.RunID)
		}
		return object{"run": a.runDetail(ctx, r)}, fmt.Sprintf("Checked run #%d — %s", r.ID, r.Status), nil

	case "wait_for_runs":
		if len(in.RunIDs) == 0 {
			return nil, "", errors.New("run_ids is required")
		}
		wait := time.Duration(in.MaxSeconds) * time.Second
		if wait <= 0 {
			wait = 5 * time.Minute
		}
		wait = min(wait, 10*time.Minute)
		deadline := time.Now().Add(wait)
		for {
			runs := []object{}
			done := true
			for _, id := range in.RunIDs {
				r, err := a.store.RunByID(ctx, id)
				if err != nil {
					return nil, "", fmt.Errorf("no run %d", id)
				}
				d := a.runDetail(ctx, r)
				if r.FinishedAt == nil {
					// Queued on a machine that is not connected: waiting
					// cannot help, it starts when the machine comes back.
					if rn, err := a.store.RunnerByID(ctx, r.RunnerID); r.Status == "queued" && err == nil && !rn.Online {
						d["note"] = rn.Name + " is offline; the run starts when it reconnects"
					} else {
						done = false
					}
				}
				runs = append(runs, d)
			}
			if done || time.Now().After(deadline) {
				states := []string{}
				for _, r := range runs {
					states = append(states, fmt.Sprintf("#%v %v", r["id"], r["status"]))
				}
				return object{"runs": runs, "timed_out": !done}, "Waited for runs: " + strings.Join(states, ", "), nil
			}
			select {
			case <-ctx.Done():
				return nil, "", ctx.Err()
			case <-time.After(5 * time.Second):
			}
		}
	}
	return nil, "", fmt.Errorf("unknown tool %q", name)
}

func (a *Assistant) repo(ctx context.Context, today, project, name string) (*store.Repo, error) {
	p, err := a.store.ProjectByKey(ctx, today, project)
	if err != nil {
		return nil, fmt.Errorf("no project %q (list_projects shows the keys)", project)
	}
	names := []string{}
	for i := range p.Repos {
		if strings.EqualFold(p.Repos[i].Name, name) {
			return &p.Repos[i], nil
		}
		names = append(names, p.Repos[i].Name)
	}
	return nil, fmt.Errorf("%s has no repo %q (it has: %s)", p.Key, name, strings.Join(names, ", "))
}

// machine picks the named machine, or the master, or (for a command only
// another machine has) that machine.
func (a *Assistant) machine(ctx context.Context, name, command string) (*store.Runner, error) {
	runners, err := a.store.ListRunners(ctx)
	if err != nil {
		return nil, err
	}
	if len(runners) == 0 {
		return nil, errors.New("no machines are set up yet")
	}
	if name != "" {
		for i := range runners {
			if strings.EqualFold(runners[i].Name, name) {
				return &runners[i], nil
			}
		}
		return nil, fmt.Errorf("no machine %q", name)
	}
	if command != "" {
		for i := range runners {
			if _, ok := runners[i].Capabilities.Command(command); ok && runners[i].Role == "master" {
				return &runners[i], nil
			}
		}
		for i := range runners {
			if _, ok := runners[i].Capabilities.Command(command); ok {
				return &runners[i], nil
			}
		}
		return nil, fmt.Errorf("no machine has a command %q", command)
	}
	for i := range runners {
		if runners[i].Role == "master" {
			return &runners[i], nil
		}
	}
	return &runners[0], nil
}

func runSummary(r *store.Run, withResult bool) object {
	o := object{"id": r.ID, "status": r.Status, "kind": r.Kind, "machine": r.RunnerName, "project": r.ProjectKey,
		"repo": r.RepoName, "created_at": r.CreatedAt.Format(time.RFC3339)}
	if r.Kind == "agent" {
		o["prompt"] = clip(r.Prompt, 300)
	} else {
		o["command"] = r.Command
	}
	if r.TaskRef != nil {
		o["task"] = *r.TaskRef
	}
	if withResult {
		if r.Result != "" {
			o["result"] = clip(r.Result, 6000)
		}
		if r.Error != "" {
			o["error"] = clip(r.Error, 1000)
		}
		if r.CostUSD != nil {
			o["cost_usd"] = *r.CostUSD
		}
	}
	return o
}

// runDetail is a run with its outcome; for a command (which has no Claude
// result) the tail of its output stands in.
func (a *Assistant) runDetail(ctx context.Context, r *store.Run) object {
	o := runSummary(r, true)
	o["runner_name"], o["repo_name"] = r.RunnerName, r.RepoName
	if r.FinishedAt != nil && r.Result == "" {
		if evs, err := a.store.ListEvents(ctx, r.ID, 0, 2000); err == nil {
			var lines []string
			for _, e := range evs {
				if e.Kind != "stdout" && e.Kind != "stderr" {
					continue
				}
				var t struct {
					Text string `json:"text"`
				}
				if json.Unmarshal(e.Data, &t) == nil && t.Text != "" {
					lines = append(lines, strings.TrimRight(t.Text, "\n"))
				}
			}
			if n := len(lines); n > 40 {
				lines = lines[n-40:]
			}
			if len(lines) > 0 {
				o["output_tail"] = clip(strings.Join(lines, "\n"), 4000)
			}
		}
	}
	if s, ok := o["result"].(string); ok {
		o["result_excerpt"] = clip(s, 600)
	}
	return o
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
