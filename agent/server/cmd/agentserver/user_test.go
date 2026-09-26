package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jyothri/bhandaar/agent/server/internal/auth"
	"github.com/jyothri/bhandaar/agent/server/internal/testdb"
)

func TestMain(m *testing.M) {
	auth.DefaultParams = auth.Params{Memory: 64, Time: 1, Threads: 1}
	os.Exit(m.Run())
}

// lines answers password prompts from a pipe, as terminalPasswords does when
// stdin isn't a terminal.
func lines(s string) readPassword {
	return terminalPasswords(strings.NewReader(s), &bytes.Buffer{})
}

func TestUserCommands(t *testing.T) {
	st := testdb.New(t)
	ctx := context.Background()
	run := func(input string, args ...string) (string, error) {
		var out bytes.Buffer
		err := userCmd(ctx, st, args, lines(input), &out)
		return out.String(), err
	}

	if _, err := run("a long password\na long password\n", "add", "--username", "jyothri"); err != nil {
		t.Fatal(err)
	}
	u, err := st.UserByName(ctx, "jyothri")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := auth.VerifyPassword(u.PasswordHash, "a long password"); !ok {
		t.Error("stored hash doesn't verify")
	}

	if _, err := run("a long password\na long password\n", "add", "--username", "jyothri"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("duplicate add: %v", err)
	}
	if _, err := run("short\nshort\n", "add", "--username", "bob"); err == nil || strings.Contains(err.Error(), "short") {
		t.Errorf("short password: %v (and it must not echo the password)", err)
	}
	if _, err := run("a long password\nanother password\n", "add", "--username", "bob"); err == nil || !strings.Contains(err.Error(), "don't match") {
		t.Errorf("mismatch: %v", err)
	}
	if _, err := run("", "add"); err == nil {
		t.Error("add without --username should fail")
	}

	if _, err := run("a new password!\na new password!\n", "passwd", "--username", "jyothri"); err != nil {
		t.Fatal(err)
	}
	u, _ = st.UserByName(ctx, "jyothri")
	if ok, _ := auth.VerifyPassword(u.PasswordHash, "a new password!"); !ok {
		t.Error("passwd didn't change the hash")
	}
	if _, err := run("a new password!\na new password!\n", "passwd", "--username", "nobody"); err == nil || !strings.Contains(err.Error(), "no user") {
		t.Errorf("passwd of unknown user: %v", err)
	}

	out, err := run("", "disable", "--username", "jyothri")
	if err != nil || !strings.Contains(out, "disabled jyothri") {
		t.Fatalf("disable: %q, %v", out, err)
	}
	out, err = run("", "list")
	if err != nil || !strings.Contains(out, "jyothri") || !strings.Contains(out, "disabled") {
		t.Fatalf("list: %q, %v", out, err)
	}
	if _, err := run("", "frobnicate"); err == nil {
		t.Error("unknown subcommand should fail")
	}
}

func TestRunUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(context.Background(), []string{"nope"}, strings.NewReader(""), &out, &errOut); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if code := run(context.Background(), []string{"help"}, strings.NewReader(""), &out, &errOut); code != 0 || !strings.Contains(out.String(), "usage") {
		t.Errorf("help: exit %d, %q", code, out.String())
	}
}
