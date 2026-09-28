// Command forge is the whole product in one binary: the server (API + web
// app), the agent that runs on your machines, and the admin commands.
//
//	forge server                               API + web app (needs DATABASE_URL)
//	forge agent [-config path]                 run the agent (runner) on this machine
//	forge agent mcp                            MCP server for Claude Code (stdio)
//	forge agent context [--hook]               Forge context for the current directory
//	forge create-user <username> <email>       create an account with a random password
//	forge reset-password <username>            replace the password with a random one
//	forge disable-2fa <username>               turn two-factor off (lost phone)
//	forge create-runner <name>                 register an agent and print its token (once)
//	forge import < data.json                   add projects/servers/vault items (skips existing)
//	forge migrate                              apply database migrations and exit
//	forge version
//
// Server commands read their configuration from the environment (see
// .env.example); the agent reads ~/.config/forge/runner.json.
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
	"syscall"
	"time"
	_ "time/tzdata" // the runtime image has no zoneinfo; timezones drive "today"

	"github.com/devai-io/forge/internal/api"
	"github.com/devai-io/forge/internal/auth"
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

Usage:
  forge server                           run the API and web app
  forge agent [-config path]             run the agent on this machine
  forge agent mcp | context [--hook]     Claude Code integration (MCP server / context hook)
  forge create-user <username> <email>   create an account (prints a random password)
  forge reset-password <username>        set a new random password
  forge disable-2fa <username>           turn two-factor off
  forge create-runner <name>             register an agent, print its token
  forge import < data.json               add projects, servers, vault items
  forge migrate                          apply database migrations
  forge version

Configuration: environment variables for the server (see .env.example),
~/.config/forge/runner.json for the agent. Docs: https://github.com/devai-io/forge
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
	default:
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
		err = runServer(os.Args[1:])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// runAgent is the machine side: `forge agent`, `forge agent mcp`,
// `forge agent context --hook`.
func runAgent(args []string) error {
	fs := flag.NewFlagSet("agent", flag.ExitOnError)
	configPath := fs.String("config", runner.DefaultConfigPath(), "path to runner.json")
	_ = fs.Parse(args)
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

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
		return fmt.Errorf("unknown agent command %q", fs.Arg(0))
	}
	if err != nil {
		return err
	}
	runner.Version = version
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

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	st := store.New(pool)
	st.PublicURL = cfg.PublicURL

	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
	}
	switch cmd {
	case "serve", "server":
		return serve(ctx, cfg, st)
	case "migrate":
		fmt.Println("migrations applied")
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
	case "import":
		raw, err := io.ReadAll(io.LimitReader(os.Stdin, 64<<20))
		if err != nil {
			return err
		}
		box, err := vault.Load(cfg.DataDir)
		if err != nil {
			return err
		}
		rep, err := st.Import(ctx, box, raw, "cli")
		if rep != nil {
			fmt.Printf("servers added: %v\nprojects added: %v\nserver notes appended: %v\nvault items added: %d\nskipped: %v\n",
				rep.ServersAdded, rep.ProjectsAdded, rep.NotesAppended, len(rep.VaultAdded), rep.Skipped)
		}
		return err
	case "create-runner":
		if len(args) != 2 {
			return errors.New("usage: forge create-runner <name>")
		}
		token, hash := auth.Token("frg_")
		rn, err := st.CreateRunner(ctx, args[1], hash)
		if err != nil {
			return err
		}
		fmt.Printf("runner %s (id %d)\ntoken: %s\nput it in ~/.config/forge/runner.json on that machine; it is not shown again.\n", rn.Name, rn.ID, token)
		return nil
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
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func serve(ctx context.Context, cfg config.Config, st *store.Store) error {
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
		slog.Warn("no accounts yet — create one with `forge create-user <username> <email>`")
	}

	mailer := mail.New(cfg.SMTP)
	if !mailer.Enabled() {
		slog.Warn("SMTP not configured: password-reset e-mails are off (use `forge reset-password`)")
	}
	mon := monitor.New(st, cfg.MonitorInterval, cfg.Version)
	go mon.Run(ctx)

	box, err := vault.Load(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("vault: %w", err)
	}
	if !box.Available() {
		slog.Warn("no vault key (VAULT_KEY_FILE): the vault and two-factor are off")
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
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second, // > the runner's 25 s long-poll
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.ListenAddr, "public_url", cfg.PublicURL, "version", cfg.Version)
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
	u, err := st.CreateUser(ctx, username, email, username, hash, cfg.DefaultTimezone)
	if err != nil {
		return err
	}
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
	if err := st.SetPassword(ctx, u.ID, hash, 0); err != nil {
		return err
	}
	fmt.Printf("new password for %s: %s\nevery session was signed out.\n", u.Username, password)
	return nil
}
