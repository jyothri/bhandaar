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
	exitBusy    = 5   // another driveagent holds a lock this one needs
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

// withSignals returns two contexts. ctx is cancelled, with a signalCause,
// on the first SIGINT or SIGTERM: the work stops gracefully (a scan records
// what it has seen, then uploads it). drain is cancelled on a second
// signal, which aborts that upload at once; the exit code still comes from
// the first. After the second signal the default handling is restored, so
// a third ends the process whatever it's doing.
func withSignals(parent context.Context) (ctx, drain context.Context, stop func()) {
	ctx, cancel := context.WithCancelCause(parent)
	drain, cancelDrain := context.WithCancelCause(parent)
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case sig := <-ch:
			cancel(signalCause{sig: sig})
		case <-done:
			return
		}
		select {
		case sig := <-ch:
			signal.Stop(ch)
			cancelDrain(signalCause{sig: sig})
		case <-done:
		}
	}()
	return ctx, drain, func() {
		close(done)
		signal.Stop(ch)
		cancel(nil)
		cancelDrain(nil)
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
