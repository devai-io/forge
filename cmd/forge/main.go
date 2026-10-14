// Command forge is the whole product in one binary: the server (API + web
// app), the agent that runs on your machines, and the admin commands.
//
// The server keeps everything in its workspace folder, FORGE_HOME (default
// ~/.config/forge): config.json, the SQLite database forge.db, vault.key,
// backups/ and projects/. The agent's settings are ~/.config/forge/agent.json.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // the runtime image has no zoneinfo; timezones drive "today"

	"github.com/devai-io/forge/internal/api"
	"github.com/devai-io/forge/internal/auth"
	"github.com/devai-io/forge/internal/backup"
	"github.com/devai-io/forge/internal/checkup"
	"github.com/devai-io/forge/internal/config"
	"github.com/devai-io/forge/internal/db"
	"github.com/devai-io/forge/internal/llm"
	"github.com/devai-io/forge/internal/mail"
	"github.com/devai-io/forge/internal/monitor"
	"github.com/devai-io/forge/internal/monitoring"
	"github.com/devai-io/forge/internal/runner"
	"github.com/devai-io/forge/internal/seed"
	"github.com/devai-io/forge/internal/store"
	"github.com/devai-io/forge/internal/vault"
)

// version is set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

const usage = `forge — projects, machines and Claude Code sessions in one place.

Server (workspace: FORGE_HOME, default ~/.config/forge):
  forge server                           run the API and web app
  forge setup-token                      print the first-run setup link
  forge add-machine <name> [role]        register a machine, print its pairing code
  forge create-user <username> <email>   create an account (prints a random password)
  forge reset-password <username>        set a new random password
  forge disable-2fa <username>           turn two-factor off
  forge backup                           snapshot the database into backups/ now
  forge openrouter status | test         the AI provider (OpenRouter): key, URL, one tiny call
  forge openrouter set-key < key.txt     store the one provider key (OpenRouter engine, Jev)
  forge openrouter url <base-url>        another OpenAI-compatible gateway (default OpenRouter)
  forge jev status | on | off            token saving with Jev (through the provider)
  forge assistant status | on | off      the Assistant (a chat that runs Forge and delegates)
  forge assistant engine claude|openrouter  what a new chat starts on (default claude)
  forge assistant model <id>             the OpenRouter engine's model
  forge assistant ask [-engine E] [-model M] [-effort E] <message>
                                         one turn from the shell: the answer and what it spent
  forge engine status | claude | openrouter  default engine for agent runs (default claude;
                                         deepseek, the old name, still works)
  forge engine model <id> [heavy-id]     the OpenRouter engine's models (default / heavy tasks)
  forge telegram status | on | off | set-token | chat <id> | test
                                         the Telegram bot (token on stdin for set-token)
  forge import < data.json               add projects, servers, vault items
  forge migrate                          apply database migrations

Machine (settings: ~/.config/forge/agent.json):
  forge agent pair <server-url> <code>   connect this machine (code from the Agents page)
  forge agent install | uninstall        run the agent in the background, at login
  forge agent setup-claude               add the Forge MCP server + hook to Claude Code
  forge agent [-config path]             run the agent in the foreground
  forge agent mcp | context [--hook]     Claude Code integration (used by setup-claude)
  forge agent secrets scan               list this checkout's git-ignored secret files
  forge agent secrets push [--yes] [FILE…]
                                         store them in the vault (Vault → Set up a machine)
  forge agent sessions [--json]          the coding-agent CLIs here (Claude Code, Codex,
                                         cursor-agent): installed, logged in, version

  forge version

Docs: https://github.com/devai-io/forge
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "agent", "runner":
		err = runAgent(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println(version)
		return
	case "help", "--help", "-h":
		fmt.Print(usage)
		return
	case "server", "serve":
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
		err = runServer(os.Args[1:])
	default:
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
		err = runServer(os.Args[1:])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// runAgent is the machine side.
func runAgent(args []string) error {
	fs := flag.NewFlagSet("agent", flag.ExitOnError)
	configPath := fs.String("config", runner.DefaultConfigPath(), "path to agent.json")
	_ = fs.Parse(args)
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	runner.Version = version

	switch fs.Arg(0) {
	case "pair":
		if fs.NArg() != 3 {
			return errors.New("usage: forge agent pair <server-url> <code>")
		}
		res, err := runner.Pair(context.Background(), fs.Arg(1), fs.Arg(2), *configPath)
		if err != nil {
			return err
		}
		fmt.Printf("Paired: this machine is %q (%s) on %s.\nSettings: %s\n\nNext:\n  forge agent install        run it in the background\n  forge agent setup-claude   connect Claude Code to Forge\n",
			res.Name, res.Role, strings.TrimRight(fs.Arg(1), "/"), *configPath)
		return nil
	case "install":
		return runner.InstallService(*configPath, os.Stdout)
	case "uninstall":
		return runner.UninstallService(os.Stdout)
	case "setup-claude":
		claude := ""
		if cfg, err := runner.LoadConfig(*configPath); err == nil {
			claude = cfg.ClaudePath
		}
		return runner.SetupClaude(*configPath, claude, os.Stdout)
	}

	cfg, err := runner.LoadConfig(*configPath)
	switch fs.Arg(0) {
	case "mcp":
		if err != nil {
			return fmt.Errorf("forge mcp: %w", err)
		}
		return runner.ServeMCP(cfg, os.Stdin, os.Stdout)
	case "context":
		if err == nil { // no config = no context; a hook must never fail a session
			runner.PrintContext(cfg, fs.Arg(1) == "--hook", os.Stdin, os.Stdout)
		}
		return nil
	case "match":
		if err == nil { // the UserPromptSubmit hook: never fails a prompt
			runner.PrintMatch(cfg, os.Stdin, os.Stdout)
		}
		return nil
	case "secrets":
		return agentSecrets(cfg, err, fs.Args()[1:])
	case "sessions":
		return agentSessions(cfg, err, fs.Args()[1:])
	case "", "run":
	default:
		return fmt.Errorf("unknown agent command %q (see forge help)", fs.Arg(0))
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w\nthis machine is not paired yet: forge agent pair <server-url> <code>", err)
		}
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runner.New(cfg).Run(ctx)
}

// agentSessions is `forge agent sessions [--json]`: the coding-agent CLIs
// on this machine and whether each is logged in, as the agent reports them
// to Forge. Works before pairing too (default paths).
func agentSessions(cfg *runner.Config, cfgErr error, args []string) error {
	fs := flag.NewFlagSet("forge agent sessions", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if cfgErr != nil {
		cfg = &runner.Config{ClaudePath: "claude", CodexPath: "codex", CursorPath: "cursor-agent"}
	}
	sessions := runner.ProbeSessions(context.Background(), cfg)
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(sessions)
	}
	names := map[string]string{"claude": "Claude Code", "codex": "Codex CLI", "cursor": "Cursor (cursor-agent)"}
	for _, s := range sessions {
		state := map[string]string{runner.SessionMissing: "not installed"}[s.State]
		if state == "" {
			state = strings.ReplaceAll(s.State, "_", " ")
		}
		line := fmt.Sprintf("%-22s %-13s", names[s.CLI], state)
		if s.Version != "" {
			line += " v" + strings.TrimPrefix(s.Version, "v")
		}
		if s.Detail != "" {
			line += " (" + s.Detail + ")"
		}
		fmt.Println(strings.TrimRight(line, " "))
	}
	return nil
}

// agentSecrets is `forge agent secrets scan|push`.
func agentSecrets(cfg *runner.Config, cfgErr error, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return errors.New("usage: forge agent secrets scan | push [--yes] [FILE…]")
	}
	switch args[0] {
	case "scan":
		return runner.SecretsScan(cwd, os.Stdout)
	case "push":
		if cfgErr != nil {
			return fmt.Errorf("%w\nthis machine is not paired yet: forge agent pair <server-url> <code>", cfgErr)
		}
		yes := false
		var files []string
		for _, a := range args[1:] {
			switch a {
			case "--yes", "-y":
				yes = true
			default:
				files = append(files, a)
			}
		}
		return runner.SecretsPush(cfg, cwd, files, yes, os.Stdin, os.Stdout)
	}
	return fmt.Errorf("unknown secrets command %q: scan or push", args[0])
}

func runServer(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.Version == "dev" {
		cfg.Version = version
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	database, err := db.Open(ctx, cfg.DBPath())
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer database.Close()
	if err := db.Migrate(ctx, database); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	st := store.New(database)
	st.PublicURL = cfg.PublicURL

	switch args[0] {
	case "serve", "server":
		return serve(ctx, cfg, database, st)
	case "migrate":
		fmt.Println("migrations applied to", cfg.DBPath())
		return nil
	case "setup-token":
		n, err := st.CountUsers(ctx)
		if err != nil {
			return err
		}
		if n > 0 {
			return errors.New("setup is done (an account exists); use `forge reset-password <username>` if you are locked out")
		}
		token, err := api.EnsureSetupToken(cfg.Home)
		if err != nil {
			return err
		}
		fmt.Printf("%s/setup?token=%s\n", cfg.PublicURL, token)
		return nil
	case "create-user":
		if len(args) != 3 {
			return errors.New("usage: forge create-user <username> <email>")
		}
		return createUser(ctx, cfg, st, args[1], args[2])
	case "reset-password":
		if len(args) != 2 {
			return errors.New("usage: forge reset-password <username>")
		}
		return resetPassword(ctx, st, args[1])
	case "backup":
		path, err := backup.Now(ctx, database, cfg.BackupDir(), max(cfg.BackupKeep, 1))
		if err != nil {
			return err
		}
		fmt.Println("wrote", path)
		return nil
	case "import":
		raw, err := io.ReadAll(io.LimitReader(os.Stdin, 64<<20))
		if err != nil {
			return err
		}
		box, err := vault.Load(cfg.Home)
		if err != nil {
			return err
		}
		rep, err := st.Import(ctx, box, raw, "cli")
		if rep != nil {
			fmt.Printf("servers added: %v\nprojects added: %v\nserver notes appended: %v\nvault items added: %d\nskipped: %v\n",
				rep.ServersAdded, rep.ProjectsAdded, rep.NotesAppended, len(rep.VaultAdded), rep.Skipped)
		}
		return err
	case "openrouter", "provider":
		return providerCommand(ctx, cfg, st, args[1:])
	case "jev":
		return jevCommand(ctx, cfg, st, args[1:])
	case "assistant":
		return assistantCommand(ctx, cfg, st, args[1:])
	case "engine":
		return engineCommand(ctx, cfg, st, args[1:])
	case "telegram":
		return telegramCommand(ctx, cfg, st, args[1:])
	case "add-machine", "create-runner":
		if len(args) < 2 || len(args) > 3 {
			return errors.New("usage: forge add-machine <name> [master|ios|worker]")
		}
		return addMachine(ctx, cfg, st, args[1], append(args[2:], "")[0])
	case "disable-2fa":
		if len(args) != 2 {
			return errors.New("usage: forge disable-2fa <username>")
		}
		u, err := st.UserByLogin(ctx, args[1])
		if err != nil {
			return fmt.Errorf("user %q: %w", args[1], err)
		}
		if err := st.DisableTOTP(ctx, u.ID); err != nil {
			return err
		}
		fmt.Printf("two-factor disabled for %s; enrol again under Settings.\n", u.Username)
		return nil
	default:
		return fmt.Errorf("unknown command %q (see forge help)", args[0])
	}
}

func serve(ctx context.Context, cfg config.Config, database *db.DB, st *store.Store) error {
	if cfg.SeedOnEmpty {
		seeded, err := st.SeedIfEmpty(ctx, seed.Demo)
		if err != nil {
			return fmt.Errorf("seed: %w", err)
		}
		if seeded {
			slog.Info("loaded the demo data into an empty database")
		}
	}
	if n, err := st.CountUsers(ctx); err == nil && n == 0 {
		token, err := api.EnsureSetupToken(cfg.Home)
		if err != nil {
			return fmt.Errorf("setup token: %w", err)
		}
		link := fmt.Sprintf("%s/setup?token=%s", cfg.PublicURL, token)
		slog.Warn("first run: open the setup link to create your account", "url", link)
		fmt.Fprintf(os.Stderr, "\n  Forge is ready. Create your account at:\n\n    %s\n\n  (forge setup-token prints this again)\n\n", link)
	}

	if err := st.ResetBusyChats(ctx); err != nil {
		return fmt.Errorf("chats: %w", err)
	}
	mailer := mail.New(cfg.SMTP)
	if !mailer.Enabled() {
		slog.Warn("SMTP not configured: e-mails are off (use `forge reset-password` if locked out)")
	}
	mon := monitor.New(st, cfg.MonitorInterval, cfg.Version)
	go mon.Run(ctx)
	go backup.Loop(ctx, database, cfg.BackupDir(), cfg.BackupKeep)

	box, err := vault.Load(cfg.Home)
	if err != nil {
		return fmt.Errorf("vault: %w", err)
	}
	if !box.Available() {
		slog.Warn("no vault key: the vault and two-factor are off")
	}
	fleet := monitoring.New(monitoring.Config{
		VictoriaMetricsURL: cfg.VictoriaMetricsURL,
		GrafanaURL:         cfg.GrafanaURL,
		GrafanaPublicURL:   cfg.GrafanaPublicURL,
	}, api.MonitoringSources{Store: st, Box: box})
	checkups := checkup.New(st, fleet, mailer, cfg.PublicURL)
	app := api.New(cfg, st, mailer, mon, api.Deps{Box: box, Mon: fleet, Checkups: checkups})
	checkups.Notify = app.NotifyCheckup
	go checkups.Loop(ctx)
	go app.RunTelegram(ctx)
	go app.RunFocus(ctx)
	go app.RunBots(ctx)

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       10 * time.Minute, // project file uploads
		WriteTimeout:      60 * time.Second, // > the agent's 25 s long-poll
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.ListenAddr, "public_url", cfg.PublicURL, "home", cfg.Home, "version", cfg.Version)
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}

func createUser(ctx context.Context, cfg config.Config, st *store.Store, username, email string) error {
	password := auth.GeneratePassword()
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	u, err := st.CreateUser(ctx, username, email, username, hash, cfg.DefaultTimezone, true)
	if err != nil {
		return err
	}
	_ = os.Remove(api.SetupTokenFile(cfg.Home))
	fmt.Printf("created %s <%s>\npassword: %s\nchange it under Settings after signing in.\n", u.Username, u.Email, password)
	return nil
}

func resetPassword(ctx context.Context, st *store.Store, username string) error {
	u, err := st.UserByLogin(ctx, username)
	if err != nil {
		return fmt.Errorf("user %q: %w", username, err)
	}
	password := auth.GeneratePassword()
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	if err := st.SetPassword(ctx, u.ID, hash, 0, true); err != nil {
		return err
	}
	fmt.Printf("new password for %s: %s\nevery session was signed out.\n", u.Username, password)
	return nil
}

// addMachine registers a machine (or finds it by name) and prints a pairing
// code for it — the Agents page's "Add machine", for a headless server.
func addMachine(ctx context.Context, cfg config.Config, st *store.Store, name, role string) error {
	var rn *store.Runner
	runners, err := st.ListRunners(ctx)
	if err != nil {
		return err
	}
	for i := range runners {
		if strings.EqualFold(runners[i].Name, name) {
			rn = &runners[i]
		}
	}
	if rn == nil {
		_, unused := auth.Token("frg_")
		if rn, err = st.CreateRunner(ctx, name, unused); err != nil {
			return err
		}
	}
	if role != "" && role != rn.Role {
		if rn, err = st.SetRunnerRole(ctx, rn.ID, role); err != nil {
			return err
		}
	}
	code := auth.PairCode()
	until, err := st.SetPairCode(ctx, rn.ID, auth.HashToken(auth.NormalizePairCode(code)), 15*time.Minute)
	if err != nil {
		return err
	}
	fmt.Printf("machine %s (%s): pairing code %s, valid until %s\n\non that machine:\n  forge agent pair %s %s\n",
		rn.Name, rn.Role, code, until.Local().Format("15:04"), cfg.PublicURL, code)
	return nil
}

// readKey reads one API key from stdin.
func readKey() (string, error) {
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 4096))
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(string(raw))
	if len(key) < 16 || strings.ContainsAny(key, " \t\n") {
		return "", errors.New("expected one API key on stdin")
	}
	return key, nil
}

// errMovedKey answers the per-feature set-key commands that one provider key replaced.
var errMovedKey = errors.New("one OpenRouter key serves the Assistant, agent runs and Jev now: forge openrouter set-key < key.txt")

// providerKeyed says whether the provider key is in the vault.
func providerKeyed(ctx context.Context, st *store.Store, box *vault.Box) bool {
	if !box.Available() {
		return false
	}
	k, _ := st.IntegrationSecret(ctx, box, store.OpenRouterTag)
	return k != ""
}

// providerCommand is Settings → AI provider, for a server without a browser.
func providerCommand(ctx context.Context, cfg config.Config, st *store.Store, args []string) error {
	if len(args) == 0 || len(args) > 2 {
		return errors.New("usage: forge openrouter status | set-key (key on stdin) | url <base-url> | test")
	}
	p, err := st.ProviderSettings(ctx)
	if err != nil {
		return err
	}
	box, err := vault.Load(cfg.Home)
	if err != nil {
		return err
	}
	switch args[0] {
	case "set-key":
		key, err := readKey()
		if err != nil {
			return err
		}
		if err := st.SaveIntegrationSecret(ctx, box, "OpenRouter API key", store.OpenRouterTag, key, "cli"); err != nil {
			return err
		}
		fmt.Println("key stored in the vault (tag " + store.OpenRouterTag + ")")
	case "url":
		if len(args) != 2 {
			return errors.New("usage: forge openrouter url <base-url>")
		}
		v := strings.TrimRight(strings.TrimSpace(args[1]), "/")
		if u, err := url.Parse(v); err != nil || u.Scheme != "https" || u.Host == "" {
			return errors.New("the base URL must be https")
		}
		p.BaseURL = v
		if err := st.SetProviderSettings(ctx, p); err != nil {
			return err
		}
	case "test":
		set, _ := st.EngineSettings(ctx)
		key := func(ctx context.Context) (string, error) { return st.IntegrationSecret(ctx, box, store.OpenRouterTag) }
		tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		start := time.Now()
		msg, u, err := llm.New(p.BaseURL, key).App("Forge", cfg.PublicURL).Chat(tctx, set.Model, "off",
			[]llm.Message{{Role: "user", Content: "Reply with: ok"}}, nil)
		if err != nil {
			return err
		}
		cost := "?"
		if u.CostUSD != nil {
			cost = fmt.Sprintf("$%.6f", *u.CostUSD)
		}
		fmt.Printf("%s answered %q in %dms (%d in, %d out, %s)\n", set.Model, strings.TrimSpace(msg.Content),
			time.Since(start).Milliseconds(), u.InputTokens, u.OutputTokens, cost)
	case "status":
	default:
		return fmt.Errorf("unknown openrouter command %q", args[0])
	}
	fmt.Printf("provider: base_url=%s anthropic_url=%s key=%t\n", p.BaseURL, p.AnthropicURL(), providerKeyed(ctx, st, box))
	return nil
}

// jevCommand is Settings → Token saving, for a server without a browser.
func jevCommand(ctx context.Context, cfg config.Config, st *store.Store, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: forge jev status | on | off")
	}
	set, err := st.JevSettings(ctx)
	if err != nil {
		return err
	}
	box, err := vault.Load(cfg.Home)
	if err != nil {
		return err
	}
	switch args[0] {
	case "on", "off":
		set.Enabled = args[0] == "on"
		if err := st.SetJevSettings(ctx, set); err != nil {
			return err
		}
	case "set-key":
		return errMovedKey
	case "status":
	default:
		return fmt.Errorf("unknown jev command %q", args[0])
	}
	fmt.Printf("jev: enabled=%t key=%t model=%s routing=%t context=%t match=%t\n",
		set.Enabled, providerKeyed(ctx, st, box), set.Model, set.Routing, set.Context, set.Match)
	return nil
}

// assistantCommand is Settings → Assistant, for a server without a browser.
func assistantCommand(ctx context.Context, cfg config.Config, st *store.Store, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: forge assistant status | on | off | engine claude|openrouter | model <id> | ask [flags] <message>")
	}
	if args[0] == "ask" {
		return assistantAsk(ctx, cfg, st, args[1:])
	}
	set, err := st.AssistantSettings(ctx)
	if err != nil {
		return err
	}
	box, err := vault.Load(cfg.Home)
	if err != nil {
		return err
	}
	switch args[0] {
	case "on", "off":
		set.Enabled = args[0] == "on"
	case "engine":
		if len(args) != 2 || !store.OneOf(store.NormEngine(args[1]), store.Engines) {
			return errors.New("usage: forge assistant engine claude|openrouter")
		}
		set.Engine = store.NormEngine(args[1])
	case "model":
		if len(args) != 2 {
			return errors.New("usage: forge assistant model <id>")
		}
		set.Model = args[1]
	case "set-key":
		return errMovedKey
	case "status":
	default:
		return fmt.Errorf("unknown assistant command %q", args[0])
	}
	if args[0] != "status" {
		if err := st.SetAssistantSettings(ctx, set); err != nil {
			return err
		}
	}
	fmt.Printf("assistant: enabled=%t engine=%s openrouter_model=%s provider=%s key=%t\n",
		set.Enabled, set.Engine, set.Model, set.BaseURL, providerKeyed(ctx, st, box))
	return nil
}

// engineCommand is Settings → Agent engine, for a server without a browser.
func engineCommand(ctx context.Context, cfg config.Config, st *store.Store, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: forge engine status | claude | openrouter | model <id> [heavy-id]")
	}
	set, err := st.EngineSettings(ctx)
	if err != nil {
		return err
	}
	box, err := vault.Load(cfg.Home)
	if err != nil {
		return err
	}
	switch args[0] {
	case "openrouter", "deepseek", "claude": // deepseek: the old name of openrouter
		set.Default = store.NormEngine(args[0])
	case "model":
		if len(args) < 2 || len(args) > 3 {
			return errors.New("usage: forge engine model <id> [heavy-id]")
		}
		set.Model = args[1]
		if len(args) == 3 {
			set.HeavyModel = args[2]
		}
	case "set-key":
		return errMovedKey
	case "status":
	default:
		return fmt.Errorf("unknown engine command %q", args[0])
	}
	if args[0] != "status" {
		if err := st.SetEngineSettings(ctx, set); err != nil {
			return err
		}
	}
	fmt.Printf("engine: default=%s openrouter_model=%s heavy_model=%s provider_key=%t\n", set.Default, set.Model, set.HeavyModel, providerKeyed(ctx, st, box))
	return nil
}

// assistantAsk runs one Assistant turn from the command line and prints the
// answer and what the turn spent.
func assistantAsk(ctx context.Context, cfg config.Config, st *store.Store, args []string) error {
	fs := flag.NewFlagSet("forge assistant ask", flag.ContinueOnError)
	user := fs.String("user", "", "account to ask as (default: the first one)")
	engine := fs.String("engine", "", "claude | openrouter (default: Settings → Assistant)")
	model := fs.String("model", "", "model (default: the engine's)")
	effort := fs.String("effort", "", "openrouter: off|low|high|max; claude: low|medium|high|xhigh|max")
	edits := fs.Bool("edits", false, "claude: may edit files")
	if err := fs.Parse(args); err != nil {
		return err
	}
	content := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if content == "" {
		return errors.New("usage: forge assistant ask [-engine claude|openrouter] [-model M] [-effort E] [-edits] <message>")
	}
	box, err := vault.Load(cfg.Home)
	if err != nil {
		return err
	}
	if *user == "" {
		if err := st.DB.QueryRow(ctx, `SELECT username FROM users ORDER BY id LIMIT 1`).Scan(user); err != nil {
			return errors.New("no account yet")
		}
	}
	srv := api.New(cfg, st, nil, nil, api.Deps{Box: box})
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	chat, err := srv.Ask(ctx, *user, content, store.ChatSettings{Engine: *engine, Model: *model, Effort: *effort, Edits: *edits})
	if err != nil {
		return err
	}
	msgs, err := st.ChatMessages(ctx, chat.ID, 0)
	if err != nil {
		return err
	}
	for _, m := range msgs[1:] {
		switch {
		case m.Role == "assistant" && m.Content != "":
			fmt.Printf("\n%s\n", m.Content)
		case m.Role == "tool":
			fmt.Printf("  [%s] %s\n", m.ToolName, m.Content)
		}
	}
	if chat.LastError != "" {
		fmt.Printf("\nturn failed: %s\n", chat.LastError)
	}
	turns, err := st.ChatTurns(ctx, chat.ID)
	if err != nil || len(turns) == 0 {
		return err
	}
	t := turns[0]
	fmt.Printf("\nchat #%d · %s %s %s · %s\n", chat.ID, t.Engine, t.Model, t.Effort, t.Status)
	for _, e := range t.Usage {
		cost := "?"
		if e.CostUSD != nil {
			cost = fmt.Sprintf("$%.5f", *e.CostUSD)
		}
		fmt.Printf("  %-12s %-24s %3d calls  %8d in  %8d cached  %7d out  %s\n", e.API, e.Model, e.Calls, e.InputTokens, e.CachedTokens, e.OutputTokens, cost)
	}
	for _, r := range t.Runs {
		if t.RunID != nil && r.ID == *t.RunID {
			continue
		}
		cost := "…"
		if r.CostUSD != nil {
			cost = fmt.Sprintf("$%.5f", *r.CostUSD)
		}
		fmt.Printf("  run #%d %s %s %s (%s) %s\n", r.ID, r.Engine, r.Model, r.RepoName, r.Status, cost)
	}
	return nil
}

// telegramCommand is Settings → Telegram, for a server without a browser.
func telegramCommand(ctx context.Context, cfg config.Config, st *store.Store, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: forge telegram status | on | off | set-token (token on stdin) | chat <id> | test")
	}
	set, err := st.TelegramSettings(ctx)
	if err != nil {
		return err
	}
	box, err := vault.Load(cfg.Home)
	if err != nil {
		return err
	}
	switch args[0] {
	case "on", "off":
		set.Enabled = args[0] == "on"
	case "chat":
		if len(args) != 2 {
			return errors.New("usage: forge telegram chat <id>")
		}
		id, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil || id == 0 {
			return fmt.Errorf("chat id %q: want a number", args[1])
		}
		set.ChatID = id
	case "set-token":
		raw, err := io.ReadAll(io.LimitReader(os.Stdin, 4096))
		if err != nil {
			return err
		}
		token := strings.TrimSpace(string(raw))
		if !regexp.MustCompile(`^\d{5,15}:[A-Za-z0-9_-]{30,64}$`).MatchString(token) {
			return errors.New("expected one bot token (123456:ABC…) on stdin")
		}
		if err := st.SaveIntegrationSecret(ctx, box, "Telegram bot token", "integration:telegram", token, "cli"); err != nil {
			return err
		}
		fmt.Println("token stored in the vault (tag integration:telegram)")
	case "test":
		res := api.New(cfg, st, nil, nil, api.Deps{Box: box}).TestTelegram(ctx)
		out, _ := json.Marshal(res)
		fmt.Println(string(out))
		if ok, _ := res["ok"].(bool); !ok {
			return errors.New("telegram test failed")
		}
		return nil
	case "status":
	default:
		return fmt.Errorf("unknown telegram command %q", args[0])
	}
	if args[0] != "status" && args[0] != "set-token" {
		if err := st.SetTelegramSettings(ctx, set); err != nil {
			return err
		}
	}
	keyed := false
	if box.Available() {
		k, _ := st.IntegrationSecret(ctx, box, "integration:telegram")
		keyed = k != ""
	}
	fmt.Printf("telegram: enabled=%t token=%t chat=%d prompts=%t runs=%t checkup=%t assistant=%t\n",
		set.Enabled, keyed, set.ChatID, set.Prompts, set.Runs, set.Checkup, set.Assistant)
	if set.Enabled && keyed && set.ChatID != 0 {
		fmt.Println("the running server picks this up within 30 s")
	}
	return nil
}
