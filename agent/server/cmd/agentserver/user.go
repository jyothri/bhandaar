package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"unicode"

	"golang.org/x/term"

	"github.com/jyothri/bhandaar/agent/server/internal/auth"
	"github.com/jyothri/bhandaar/agent/server/internal/store"
)

// readPassword prompts for a password. Passwords never come from argv or the
// environment: argv shows in ps and shell history, and the environment is
// inherited by child processes.
type readPassword func(prompt string) (string, error)

// terminalPasswords reads without echo from a terminal; otherwise (a pipe, in
// scripts and tests) it reads one line per prompt.
func terminalPasswords(stdin io.Reader, prompts io.Writer) readPassword {
	if f, ok := stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		return func(prompt string) (string, error) {
			fmt.Fprint(prompts, prompt)
			b, err := term.ReadPassword(int(f.Fd()))
			fmt.Fprintln(prompts)
			return string(b), err
		}
	}
	lines := bufio.NewReader(stdin)
	return func(prompt string) (string, error) {
		fmt.Fprint(prompts, prompt)
		s, err := lines.ReadString('\n')
		if err != nil && (err != io.EOF || s == "") {
			return "", fmt.Errorf("read password: %w", err)
		}
		fmt.Fprintln(prompts)
		return strings.TrimRight(s, "\r\n"), nil
	}
}

func userCmd(ctx context.Context, st *store.Store, args []string, read readPassword, out io.Writer) error {
	if len(args) == 0 {
		return usageError("user: missing subcommand (add, passwd, disable or list)")
	}
	sub, args := args[0], args[1:]

	fs := flag.NewFlagSet("user "+sub, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	username := fs.String("username", "", "the user's name")
	if err := fs.Parse(args); err != nil {
		return usageError(err.Error())
	}
	if fs.NArg() > 0 {
		return usageError("user " + sub + ": unexpected arguments " + strings.Join(fs.Args(), " "))
	}
	needName := func() error {
		if err := validUsername(*username); err != nil {
			return usageError("user " + sub + ": " + err.Error())
		}
		return nil
	}

	switch sub {
	case "add":
		if err := needName(); err != nil {
			return err
		}
		hash, err := newPassword(read)
		if err != nil {
			return err
		}
		if _, err := st.CreateUser(ctx, *username, hash); err != nil {
			if errors.Is(err, store.ErrUserExists) {
				return fmt.Errorf("user %q already exists", *username)
			}
			return err
		}
		fmt.Fprintf(out, "added user %s\n", *username)
	case "passwd":
		if err := needName(); err != nil {
			return err
		}
		if _, err := st.UserByName(ctx, *username); err != nil {
			return userErr(*username, err)
		}
		hash, err := newPassword(read)
		if err != nil {
			return err
		}
		if err := st.SetPassword(ctx, *username, hash); err != nil {
			return userErr(*username, err)
		}
		fmt.Fprintf(out, "changed the password of %s\n", *username)
	case "disable":
		if err := needName(); err != nil {
			return err
		}
		revoked, err := st.DisableUser(ctx, *username)
		if err != nil {
			return userErr(*username, err)
		}
		fmt.Fprintf(out, "disabled %s and revoked %d refresh token(s)\n", *username, revoked)
	case "list":
		users, err := st.ListUsers(ctx)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "USERNAME\tCREATED\tSTATUS")
		for _, u := range users {
			status := "active"
			if u.Disabled() {
				status = "disabled " + u.DisabledAt.UTC().Format("2006-01-02")
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\n", u.Username, u.CreatedAt.UTC().Format("2006-01-02"), status)
		}
		return tw.Flush()
	default:
		return usageError("user: unknown subcommand " + sub)
	}
	return nil
}

func userErr(username string, err error) error {
	if errors.Is(err, store.ErrNoSuchUser) {
		return fmt.Errorf("no user %q", username)
	}
	return err
}

func validUsername(s string) error {
	if s == "" {
		return errors.New("--username is required")
	}
	if len(s) > 128 {
		return errors.New("--username: at most 128 bytes")
	}
	for _, r := range s {
		if unicode.IsSpace(r) || !unicode.IsPrint(r) {
			return errors.New("--username: no spaces or control characters")
		}
	}
	return nil
}

// newPassword prompts twice and hashes the result. Error messages never
// include the password.
func newPassword(read readPassword) (string, error) {
	p1, err := read("Password: ")
	if err != nil {
		return "", err
	}
	if len([]rune(p1)) < auth.MinPasswordLen {
		return "", fmt.Errorf("the password must be at least %d characters", auth.MinPasswordLen)
	}
	p2, err := read("Password again: ")
	if err != nil {
		return "", err
	}
	if p1 != p2 {
		return "", errors.New("the passwords don't match")
	}
	return auth.HashPassword(p1)
}
