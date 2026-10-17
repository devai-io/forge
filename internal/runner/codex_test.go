package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A real subprocess exercises pipe lifecycle, handshake ordering, resumption,
// usage, failure and cancellation without authenticating or invoking a model.
func TestCodexAppServerHelper(t *testing.T) {
	mode := os.Getenv("FORGE_CODEX_TEST_HELPER")
	if mode == "" {
		return
	}
	enc := json.NewEncoder(os.Stdout)
	send := func(v any) {
		if enc.Encode(v) != nil {
			os.Exit(2)
		}
	}
	note := func(method string, params any) { send(map[string]any{"method": method, "params": params}) }
	scanner := bufio.NewScanner(os.Stdin)
	initialized := false
	for scanner.Scan() {
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &m) != nil {
			os.Exit(3)
		}
		switch m.Method {
		case "initialize":
			send(map[string]any{"id": m.ID, "result": map[string]any{}})
		case "initialized":
			initialized = true
		case "thread/start", "thread/resume":
			if !initialized || m.Params["sandbox"] != "read-only" || m.Params["approvalPolicy"] != "never" {
				os.Exit(4)
			}
			if mode == "resume" && (m.Method != "thread/resume" || m.Params["threadId"] != "thread-test") {
				os.Exit(5)
			}
			send(map[string]any{"id": m.ID, "result": map[string]any{"thread": map[string]any{"id": "thread-test", "cwd": os.Getenv("FORGE_CODEX_TEST_DIR")}, "model": "test-model"}})
		case "turn/start":
			if m.Params["threadId"] != "thread-test" {
				os.Exit(6)
			}
			if mode == "hang" {
				time.Sleep(time.Hour)
				os.Exit(7)
			}
			if mode == "eof" {
				os.Exit(0)
			}
			note("turn/started", map[string]any{"turn": map[string]any{"id": "turn-test"}})
			note("item/completed", map[string]any{"item": map[string]any{"id": "answer", "type": "agentMessage", "text": "Finished."}})
			note("thread/tokenUsage/updated", map[string]any{"tokenUsage": map[string]any{"total": map[string]int{"inputTokens": 120, "cachedInputTokens": 20, "outputTokens": 30}}})
			status := "completed"
			if mode == "failed" {
				status = "failed"
			}
			note("turn/completed", map[string]any{"turn": map[string]any{"status": status}})
		}
	}
	os.Exit(0)
}

func TestCodexRunnerLifecycle(t *testing.T) {
	for _, mode := range []string{"success", "resume", "eof", "failed", "hang"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			wrapper := filepath.Join(dir, "codex")
			if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec \"$FORGE_CODEX_TEST_BINARY\" -test.run=TestCodexAppServerHelper -- \"$@\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "context") {
					fmt.Fprint(w, `{"markdown":"Project context"}`)
					return
				}
				w.WriteHeader(204)
			}))
			defer srv.Close()
			cfg := &Config{APIURL: srv.URL, Token: "frg_test", CodexPath: wrapper, AllowedRoots: []string{dir}, PermissionModes: []string{"plan"}, Env: map[string]string{"FORGE_CODEX_TEST_HELPER": mode, "FORGE_CODEX_TEST_BINARY": exe, "FORGE_CODEX_TEST_DIR": dir}}
			r := New(cfg)
			ship := newShipper(r.client, 1)
			defer ship.close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if mode == "hang" {
				go func() { time.Sleep(200 * time.Millisecond); cancel() }()
			}
			run := claimedRun{ID: 1, Engine: "codex", PermissionMode: "plan", Prompt: "Report status"}
			if mode == "resume" {
				run.ResumeSession = "thread-test"
				run.Setup = &engineSetup{UsageBaseline: json.RawMessage(`{"threadTotal":{"inputTokens":100,"cachedInputTokens":10,"outputTokens":20}}`)}
			}
			rep := r.runCodex(ctx, run, dir, ship)
			expected := "succeeded"
			if mode == "failed" || mode == "eof" {
				expected = "failed"
			}
			if mode == "hang" {
				expected = "cancelled"
			}
			if rep.Status != expected {
				t.Fatalf("report = %+v", rep)
			}
			if expected == "succeeded" {
				if rep.SessionID != "thread-test" || rep.Result != "Finished." {
					t.Fatalf("report = %+v", rep)
				}
				var u codexTokens
				if err := json.Unmarshal(rep.Usage, &u); err != nil {
					t.Fatal(err)
				}
				want := int64(120)
				if mode == "resume" {
					want = 20
				}
				if u.Input != want || rep.CostUSD != nil {
					t.Fatalf("usage = %s", rep.Usage)
				}
			}
		})
	}
}

type codexBuffer struct{ bytes.Buffer }

func (*codexBuffer) Close() error { return nil }

func TestCodexApprovalsStayWithinMachinePolicy(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, mode := range []string{"plan", "acceptEdits"} {
			b := &codexBuffer{}
			s := newSession(context.Background(), &Runner{cfg: &Config{Approvals: enabled}}, claimedRun{PermissionMode: mode}, nil, b)
			c := &codexSession{s: s}
			p := pendingPrompt{kind: "item/commandExecution/requestApproval", suggestions: json.RawMessage(`42`)}
			c.answer("", p, promptAnswer{Decision: "allow_always"})
			want := "decline"
			if enabled && mode != "plan" {
				want = "acceptForSession"
			}
			if !strings.Contains(b.String(), want) {
				t.Fatalf("enabled=%t mode=%s response=%s", enabled, mode, b.String())
			}
			s.end()
		}
	}
	for _, mode := range []string{"auto", "dontAsk", "unknown"} {
		if _, _, err := codexPolicy(mode, true); err == nil {
			t.Errorf("accepted unsupported mode %q", mode)
		}
	}
}

func TestReadOnlyForgeMCPRejectsUnlistedMutations(t *testing.T) {
	t.Setenv("FORGE_MCP_READ_ONLY", "1")
	for _, tool := range listedTools() {
		if !readOnlyTools[tool["name"].(string)] {
			t.Fatalf("listed mutating tool: %v", tool)
		}
	}
	result, rpcErr := handleMCP(nil, rpcMsg{Method: "tools/call", Params: json.RawMessage(`{"name":"forge_update_task","arguments":{"task":"SHOP-1","status":"done"}}`)})
	if rpcErr != nil || result.(map[string]any)["isError"] != true {
		t.Fatalf("mutation was not refused: %v %v", result, rpcErr)
	}
}

func TestCodexInteractiveFollowup(t *testing.T) {
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(dir, "codex")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec \"$FORGE_CODEX_TEST_BINARY\" -test.run=TestCodexAppServerHelper -- \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	waiting := make(chan struct{}, 4)
	var polls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "context"):
			fmt.Fprint(w, `{"markdown":"Project context"}`)
		case strings.HasSuffix(r.URL.Path, "/awaiting"):
			var v struct {
				Awaiting string `json:"awaiting"`
			}
			_ = json.NewDecoder(r.Body).Decode(&v)
			if v.Awaiting == "reply" {
				waiting <- struct{}{}
			}
			w.WriteHeader(204)
		case strings.HasSuffix(r.URL.Path, "/inbox"):
			select {
			case <-waiting:
			case <-r.Context().Done():
				return
			}
			polls++
			if polls == 1 {
				fmt.Fprint(w, `{"items":[{"id":1,"kind":"message","data":{"text":"One more turn"}}]}`)
			} else {
				fmt.Fprint(w, `{"items":[{"id":2,"kind":"end","data":{}}],"done":true}`)
			}
		default:
			w.WriteHeader(204)
		}
	}))
	defer srv.Close()
	cfg := &Config{APIURL: srv.URL, Token: "frg_test", CodexPath: wrapper, AllowedRoots: []string{dir}, PermissionModes: []string{"plan"}, IdleMinutes: 1, Env: map[string]string{"FORGE_CODEX_TEST_HELPER": "success", "FORGE_CODEX_TEST_BINARY": exe, "FORGE_CODEX_TEST_DIR": dir}}
	r := New(cfg)
	ship := newShipper(r.client, 1)
	defer ship.close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rep := r.runCodex(ctx, claimedRun{ID: 1, Engine: "codex", PermissionMode: "plan", Prompt: "First turn", Interactive: true}, dir, ship)
	if rep.Status != "succeeded" || rep.NumTurns == nil || *rep.NumTurns != 2 {
		t.Fatalf("interactive report = %+v", rep)
	}
}

func TestSetupCodexAddsForgeHooks(t *testing.T) {
	dir := t.TempDir()
	codexHome := filepath.Join(dir, "codex")
	if err := os.Mkdir(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	// Another tool's hook survives; an earlier Forge hook is replaced.
	orig := `{"hooks":{"SessionStart":[{"matcher":"startup|resume","hooks":[{"type":"command","command":"other-tool hook"}]},{"hooks":[{"type":"command","command":"/old/forge agent context --hook"}]}]}}`
	if err := os.WriteFile(filepath.Join(codexHome, "hooks.json"), []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", codexHome)
	configPath := filepath.Join(dir, "agent.json")
	cfg := fmt.Sprintf(`{"api_url":"https://forge.example.com","token":"frg_test","allowed_roots":[%q],"codex_path":"true"}`, dir)
	if err := os.WriteFile(configPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := SetupCodex(configPath, &out); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(codexHome, "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	var commands []string
	for _, event := range []string{"SessionStart", "UserPromptSubmit"} {
		for _, g := range got.Hooks[event] {
			for _, h := range g.Hooks {
				commands = append(commands, event+": "+h.Command)
			}
		}
	}
	joined := strings.Join(commands, "\n")
	if strings.Contains(joined, "/old/forge") || !strings.Contains(joined, "SessionStart: other-tool hook") ||
		!strings.Contains(joined, "agent -config "+configPath+" context --hook") ||
		!strings.Contains(joined, "UserPromptSubmit: ") || !strings.Contains(joined, "match --hook") {
		t.Fatalf("hooks:\n%s", joined)
	}
	if !strings.Contains(out.String(), "/hooks") {
		t.Fatalf("output does not say how to trust the hooks: %s", out.String())
	}
}

func TestIsCodexPane(t *testing.T) {
	if !isCodex("codex", "") || isCodex("zsh", "1") || isCodex("node", "x") {
		t.Fatal("isCodex by command name")
	}
	if _, err := os.Stat("/proc/self/task"); err != nil {
		t.Skip("no /proc")
	}
	// The npm launcher is "node" with the native codex binary under it: a
	// process tree is what gets looked at.
	cmd := exec.Command("sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	time.Sleep(50 * time.Millisecond)
	if !procTreeRuns(os.Getpid(), "sleep", 1) || procTreeRuns(os.Getpid(), "codex", 1) {
		t.Fatal("procTreeRuns does not see the child")
	}
}
