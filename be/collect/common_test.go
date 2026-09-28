package collect

import (
	"errors"
	"testing"

	"github.com/jyothri/hdd/db"
)

func TestResolveAccountTakesTheNameFromTheDatabase(t *testing.T) {
	original := linkedAccount
	linkedAccount = func(userID int64, clientKey string) (db.PrivateToken, error) {
		if userID == 7 && clientKey == "k1" {
			return db.PrivateToken{Client_key: "k1", RefreshToken: "rt", DisplayName: "jyo****ri@example.com"}, nil
		}
		return db.PrivateToken{}, errors.New("not found")
	}
	t.Cleanup(func() { linkedAccount = original })

	got, err := resolveAccount(7, "k1", "ignored")
	want := googleAccount{RefreshToken: "rt", ClientKey: "k1", Name: "jyo****ri@example.com"}
	if err != nil || got != want {
		t.Errorf("linked account: %+v, %v; want %+v", got, err, want)
	}

	// Another user's account isn't found.
	if _, err := resolveAccount(8, "k1", ""); err == nil {
		t.Error("user 8 used user 7's account")
	}

	// A raw refresh token has no account to record.
	got, err = resolveAccount(7, "", "raw")
	if err != nil || got != (googleAccount{RefreshToken: "raw"}) {
		t.Errorf("raw token: %+v, %v", got, err)
	}
	if _, err := resolveAccount(7, "", ""); err == nil {
		t.Error("no account and no token: want an error")
	}
}
