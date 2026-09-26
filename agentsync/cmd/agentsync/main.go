// Command agentsync receives driveagent scan data and stores it in Postgres.
//
//	agentsync [serve]                      run the server (the default)
//	agentsync housekeeping                 delete expired rows once, then exit
//	agentsync user add --username NAME     add a user (prompts for the password)
//	agentsync user passwd --username NAME  change a user's password
//	agentsync user disable --username NAME disable a user and revoke their tokens
//	agentsync user list                    list users
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jyothri/bhandaar/agentsync/internal/api"
	"github.com/jyothri/bhandaar/agentsync/internal/auth"
	"github.com/jyothri/bhandaar/agentsync/internal/config"
	"github.com/jyothri/bhandaar/agentsync/internal/store"
)

const usage = `usage:
  agentsync [serve]                      run the server
  agentsync housekeeping                 delete expired rows once, then exit
  agentsync user add --username NAME     add a user (prompts for the password twice)
  agentsync user passwd --username NAME  change a user's password
  agentsync user disable --username NAME disable a user and revoke their refresh tokens
  agentsync user list                    list users
`

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	cmd := "serve"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve(ctx)
	case "housekeeping":
		// The tasks arrive with the tables they clean up.
		err = withStore(ctx, func(*store.Store) error {
			slog.Info("housekeeping: no tasks yet")
			return nil
		})
	case "user":
		err = withStore(ctx, func(st *store.Store) error {
			return userCmd(ctx, st, args, terminalPasswords(stdin, stderr), stdout)
		})
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		err = usageError("unknown command " + cmd)
	}
	var uerr usageError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &uerr):
		fmt.Fprintf(stderr, "agentsync: %v\n\n%s", err, usage)
		return 2
	default:
		fmt.Fprintf(stderr, "agentsync: %v\n", err)
		return 1
	}
}

type usageError string

func (e usageError) Error() string { return string(e) }

// withStore connects, migrates and runs f, for the admin commands.
func withStore(ctx context.Context, f func(*store.Store) error) error {
	db, err := config.LoadDB(os.Getenv)
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, db)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return err
	}
	return f(st)
}

func serve(ctx context.Context) error {
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return err
	}

	tokens := &auth.Tokens{Pool: st.Pool, Secret: cfg.JWTSecret, AccessTTL: cfg.AccessTTL, RefreshTTL: cfg.RefreshTTL}
	srv := &http.Server{
		Addr:         cfg.Listen,
		Handler:      api.New(cfg, st, tokens, nil).Handler(),
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	slog.Info("agentsync listening", "addr", cfg.Listen, "version", api.ServerVersion,
		"min_agent_version", cfg.MinAgentVersion.String(), "latest_agent_version", cfg.LatestAgentVersion.String())

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
