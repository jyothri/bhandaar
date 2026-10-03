package db

import "testing"

func TestSettings(t *testing.T) {
	migrated(t)
	alice, bob := addUser(t, "alice"), addUser(t, "bob")
	if s, err := GetSettings(alice); err != nil || s.ShowGcs {
		t.Fatalf("defaults = %+v, %v; want Cloud Storage off", s, err)
	}
	if err := SaveSettings(alice, Settings{ShowGcs: true}); err != nil {
		t.Fatal(err)
	}
	if s, _ := GetSettings(alice); !s.ShowGcs {
		t.Error("alice's Cloud Storage isn't on after saving it")
	}
	if s, _ := GetSettings(bob); s.ShowGcs {
		t.Error("bob got alice's setting")
	}
	if err := SaveSettings(alice, Settings{}); err != nil {
		t.Fatal(err)
	}
	if s, _ := GetSettings(alice); s.ShowGcs {
		t.Error("alice's Cloud Storage is still on after turning it off")
	}
}
