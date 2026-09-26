package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jyothri/bhandaar/agent/client/internal/remote"
)

// Exit codes (docs/specs/remote-sync-agent.md, "Exit codes").
const (
	exitOK      = 0
	exitLocal   = 1
	exitUsage   = 2
	exitRemote  = 3
	exitUpgrade = 4
	exitSIGINT  = 130 // 128 + the signal number, as shells report it
	exitSIGTERM = 143
)

// exitError makes main exit with code.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

func usageErr(format string, a ...any) error {
	return &exitError{code: exitUsage, err: fmt.Errorf(format, a...)}
}

// remoteErr gives a remote failure its exit code: 4 for an upgrade, else 3.
func remoteErr(err error) error {
	if err == nil {
		return nil
	}
	var ee *exitError
	if errors.As(err, &ee) {
		return err
	}
	if errors.Is(err, remote.ErrUpgrade) {
		return &exitError{code: exitUpgrade, err: err}
	}
	return &exitError{code: exitRemote, err: err}
}

// signalCause is the cancel cause when the user interrupts the command.
type signalCause struct{ sig os.Signal }

func (c signalCause) Error() string { return "interrupted by " + signalName(c.sig) }

func (c signalCause) exitCode() int {
	if c.sig == syscall.SIGTERM {
		return exitSIGTERM
	}
	return exitSIGINT
}

func signalName(sig os.Signal) string {
	switch sig {
	case os.Interrupt:
		return "SIGINT (Ctrl-C)"
	case syscall.SIGTERM:
		return "SIGTERM"
	}
	return sig.String()
}

// withSignals returns a context cancelled, with a signalCause, on the first
// SIGINT or SIGTERM. The first signal stops the work gracefully (a scan
// records what it has seen); after it, the default handling is restored, so
// a second signal ends the process at once. (From M6 on, the second one will
// abort the upload drain instead.)
func withSignals(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case sig := <-ch:
			signal.Stop(ch)
			cancel(signalCause{sig: sig})
		case <-done:
		}
	}()
	return ctx, func() {
		close(done)
		signal.Stop(ch)
		cancel(nil)
	}
}

// exitCode picks the process exit code for a command's result. An
// interrupted command exits 130 (SIGINT) or 143 (SIGTERM), whatever error
// the interruption surfaced as.
func exitCode(ctx context.Context, err error) int {
	var sc signalCause
	if errors.As(context.Cause(ctx), &sc) {
		return sc.exitCode()
	}
	if err == nil {
		return exitOK
	}
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	return exitLocal
}
