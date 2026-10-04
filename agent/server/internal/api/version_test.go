package api

import "testing"

func TestShortCommit(t *testing.T) {
	for in, want := range map[string]string{
		"d5f4746a1b2c3d4e5f60718293a4b5c6d7e8f901": "d5f4746",
		"d5f4746": "d5f4746",
		"unknown": "unknown",
		"":        "",
	} {
		if got := shortCommit(in); got != want {
			t.Errorf("shortCommit(%q) = %q, want %q", in, got, want)
		}
	}
	if ServerVersion != Version+"+unknown" {
		t.Errorf("ServerVersion = %q in a test build", ServerVersion)
	}
}
