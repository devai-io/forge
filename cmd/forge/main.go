// Command forge is the whole product in one binary: the server (API + web
// app), the agent that runs on your machines, and the admin commands.
//
// The server keeps everything in its workspace folder, FORGE_HOME (default
// ~/.forge): config.json, the SQLite database forge.db, vault.key, backups/
// and projects/. The agent's settings are ~/.forge/agent.json.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
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

Server (workspace: FORGE_HOME, default ~/.forge):
  forge server                           run the API and web app
  forge setup-token                      print the first-run setup link
  forge add-machine <name> [role]        register a machine, print its pairing code
  forge create-user <username> <email>   create an account (prints a random password)
  forge reset-password <username>        set a new random password
  forge disable-2fa <username>           turn two-factor off
  forge backup                           snapshot the database into backups/ now
  forge import < data.json               add projects, servers, vault items
  forge migrate                          apply database migrations

Machine (settings: ~/.forge/agent.json):
  forge agent pair <server-url> <code>   connect this machine (code from the Agents page)
  forge agent install | uninstall        run the agent in the background, at login
  forge agent setup-claude               add the Forge MCP server + hook to Claude Code
  forge agent [-config path]             run the agent in the foreground
  forge agent mcp | context [--hook]     Claude Code integration (used by setup-claude)

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
	go checkups.Loop(ctx)

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           api.New(cfg, st, mailer, mon, api.Deps{Box: box, Mon: fleet, Checkups: checkups}).Handler(),
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
