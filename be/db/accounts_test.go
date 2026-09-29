package db

import (
	"reflect"
	"testing"
)

func TestServices(t *testing.T) {
	tests := map[string][]string{
		"": {ServiceGmail}, // linked before scopes were per service
		"https://www.googleapis.com/auth/gmail.readonly":                                                                             {ServiceGmail},
		"openid https://www.googleapis.com/auth/drive.metadata.readonly":                                                             {ServiceDrive},
		"https://www.googleapis.com/auth/drive.readonly":                                                                             {ServiceDrive},
		"https://www.googleapis.com/auth/drive.metadata.readonly https://www.googleapis.com/auth/gmail.readonly":                     {ServiceGmail, ServiceDrive},
		"openid https://www.googleapis.com/auth/userinfo.email":                                                                      {},
		"https://www.googleapis.com/auth/photoslibrary.readonly":                                                                     {},
		"https://www.googleapis.com/auth/photospicker.mediaitems.readonly":                                                           {ServicePhotos},
		"https://www.googleapis.com/auth/photospicker.mediaitems.readonly https://www.googleapis.com/auth/gmail.readonly":            {ServiceGmail, ServicePhotos},
		"openid https://www.googleapis.com/auth/devstorage.read_only https://www.googleapis.com/auth/cloudplatformprojects.readonly": {ServiceGcs},
		"https://www.googleapis.com/auth/cloud-platform.read-only https://www.googleapis.com/auth/gmail.readonly":                    {ServiceGmail, ServiceGcs},
		"https://www.googleapis.com/auth/cloudplatformprojects.readonly":                                                             {},
	}
	for scope, want := range tests {
		if got := Services(scope); !reflect.DeepEqual(got, want) {
			t.Errorf("Services(%q) = %v, want %v", scope, got, want)
		}
	}
}

func link(sub, name, refreshToken, scope string) GoogleLink {
	return GoogleLink{GoogleSub: sub, DisplayName: name, AccessToken: "at",
		RefreshToken: refreshToken, Scope: scope, TokenType: "Bearer"}
}

const (
	gmailScope = "https://www.googleapis.com/auth/gmail.readonly"
	bothScopes = gmailScope + " https://www.googleapis.com/auth/drive.metadata.readonly"
)

func TestLinkAccount(t *testing.T) {
	migrated(t)
	alice, bob := addUser(t, "alice"), addUser(t, "bob")

	// A new account gets the new key.
	key, err := LinkAccount(alice, link("sub-1", "ali****ce@example.com", "rt1", gmailScope), "key-1")
	if err != nil || key != "key-1" {
		t.Fatalf("first link = %q, %v; want key-1", key, err)
	}

	// Linking it again, now with Drive too, updates the row and keeps its key.
	key, err = LinkAccount(alice, link("sub-1", "ali****ce@example.com", "rt2", bothScopes), "key-2")
	if err != nil || key != "key-1" {
		t.Fatalf("re-link = %q, %v; want key-1", key, err)
	}
	account, err := GetOAuthToken(alice, "key-1")
	if err != nil || account.RefreshToken != "rt2" || account.Scope != bothScopes {
		t.Fatalf("after re-link: %+v, %v; want rt2 with both scopes", account, err)
	}

	// Bob linking the same Google account gets a row of his own.
	key, err = LinkAccount(bob, link("sub-1", "ali****ce@example.com", "rt3", gmailScope), "key-3")
	if err != nil || key != "key-3" {
		t.Fatalf("bob's link = %q, %v; want key-3", key, err)
	}
	if account, _ := GetOAuthToken(alice, "key-1"); account.RefreshToken != "rt2" {
		t.Errorf("bob's link changed alice's row: %+v", account)
	}

	accounts, err := GetRequestAccountsFromDb(alice)
	want := []Account{{ClientKey: "key-1", DisplayName: "ali****ce@example.com",
		Services: []string{ServiceGmail, ServiceDrive}, LoginHint: "sub-1"}}
	if err != nil || !reflect.DeepEqual(accounts, want) {
		t.Errorf("alice's accounts = %+v, %v; want %+v", accounts, err, want)
	}
}

func TestLinkAccountAdoptsTheNewestLegacyRow(t *testing.T) {
	migrated(t)
	alice, bob := addUser(t, "alice"), addUser(t, "bob")
	// Rows from before google_sub: a stale duplicate and its re-link, another
	// account, and bob's row with the same name.
	if _, err := db.Exec(`INSERT INTO privatetokens
			(access_token, refresh_token, display_name, client_key, created_on, scope, expires_in, token_type, user_id)
		VALUES ('at', 'old', 'jyo****ri@example.com', 'stale', now(), $1, 0, 'Bearer', $2),
		       ('at', 'rt', 'jyo****ri@example.com', 'current', now(), $1, 0, 'Bearer', $2),
		       ('at', 'rt', 'oth****er@example.com', 'other', now(), NULL, NULL, NULL, $2),
		       ('at', 'rt', 'jyo****ri@example.com', 'bobs', now(), $1, 0, 'Bearer', $3)`,
		gmailScope, alice, bob); err != nil {
		t.Fatal(err)
	}

	key, err := LinkAccount(alice, link("sub-j", "jyo****ri@example.com", "new", bothScopes), "unused")
	if err != nil || key != "current" {
		t.Fatalf("link = %q, %v; want the newest legacy row, current", key, err)
	}
	for clientKey, wantToken := range map[string]string{"current": "new", "stale": "old"} {
		if account, err := GetOAuthToken(alice, clientKey); err != nil || account.RefreshToken != wantToken {
			t.Errorf("%s: %+v, %v; want refresh token %q", clientKey, account, err, wantToken)
		}
	}
	if account, _ := GetOAuthToken(bob, "bobs"); account.RefreshToken != "rt" {
		t.Errorf("bob's row changed: %+v", account)
	}

	// Once adopted, the row is found by its sub, whatever the name.
	if key, err := LinkAccount(alice, link("sub-j", "renamed", "newer", bothScopes), "unused"); err != nil || key != "current" {
		t.Errorf("link by sub = %q, %v; want current", key, err)
	}

	accounts, err := GetRequestAccountsFromDb(alice)
	if err != nil || len(accounts) != 3 {
		t.Fatalf("accounts = %+v, %v; want 3", accounts, err)
	}
	// A NULL scope counts as Gmail; legacy rows have no login hint.
	if got := accounts[2]; got.ClientKey != "other" || !reflect.DeepEqual(got.Services, []string{ServiceGmail}) || got.LoginHint != "" {
		t.Errorf("legacy account = %+v", got)
	}
}

func TestLinkAccountNeedsASub(t *testing.T) {
	if _, err := LinkAccount(1, link("", "x", "rt", gmailScope), "k"); err == nil {
		t.Error("LinkAccount without a Google account ID succeeded")
	}
}

// scanOf records a scan of userID, as the collectors do.
func scanOf(t *testing.T, userID int64, name, clientKey, filter string) int {
	t.Helper()
	scanId, err := LogStartScan("gmail", userID)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveScanMetadata(name, clientKey, "", filter, scanId); err != nil {
		t.Fatal(err)
	}
	return scanId
}

func scanIds(t *testing.T, userID int64, clientKey string) []int {
	t.Helper()
	requests, err := GetScanRequestsFromDb(userID, clientKey)
	if err != nil {
		t.Fatal(err)
	}
	ids := []int{}
	for _, r := range requests {
		ids = append(ids, r.Id)
	}
	return ids
}

func TestHistoryGroupsByAccountNotName(t *testing.T) {
	migrated(t)
	alice, bob := addUser(t, "alice"), addUser(t, "bob")
	// Two of alice's Google accounts that mask to the same name.
	for _, sub := range []string{"sub-1", "sub-2"} {
		if _, err := LinkAccount(alice, link(sub, "jyo****ri@example.com", "rt", gmailScope), "key-"+sub); err != nil {
			t.Fatal(err)
		}
	}
	s1 := scanOf(t, alice, "jyo****ri@example.com", "key-sub-1", "a")
	s2 := scanOf(t, alice, "jyo****ri@example.com", "key-sub-2", "b")
	s3 := scanOf(t, alice, "jyo****ri@example.com", "key-sub-1", "c")
	scanOf(t, alice, "", "", "") // a local scan: no account

	if got := scanIds(t, alice, "key-sub-1"); !reflect.DeepEqual(got, []int{s3, s1}) {
		t.Errorf("key-sub-1 scans = %v, want %v", got, []int{s3, s1})
	}
	if got := scanIds(t, alice, "key-sub-2"); !reflect.DeepEqual(got, []int{s2}) {
		t.Errorf("key-sub-2 scans = %v, want %v", got, []int{s2})
	}
	if got := scanIds(t, bob, "key-sub-1"); len(got) != 0 {
		t.Errorf("bob sees alice's scans %v", got)
	}

	accounts, err := GetAccountsFromDb(alice)
	want := []ScannedAccount{
		{ClientKey: "key-sub-1", DisplayName: "jyo****ri@example.com"},
		{ClientKey: "key-sub-2", DisplayName: "jyo****ri@example.com"},
	}
	if err != nil || !reflect.DeepEqual(accounts, want) {
		t.Errorf("alice's scanned accounts = %+v, %v; want %+v", accounts, err, want)
	}
	if accounts, _ := GetAccountsFromDb(bob); len(accounts) != 0 {
		t.Errorf("bob's scanned accounts = %+v, want none", accounts)
	}
}

func TestScannedAccountIsNamedByItsNewestScan(t *testing.T) {
	migrated(t)
	alice := addUser(t, "alice")
	scanOf(t, alice, "old-name", "k1", "")
	scanOf(t, alice, "new-name", "k1", "")

	accounts, err := GetAccountsFromDb(alice)
	if err != nil || !reflect.DeepEqual(accounts, []ScannedAccount{{ClientKey: "k1", DisplayName: "new-name"}}) {
		t.Errorf("accounts = %+v, %v", accounts, err)
	}
}

func TestBackfillScanAccounts(t *testing.T) {
	migrated(t)
	alice, bob := addUser(t, "alice"), addUser(t, "bob")
	// Linked accounts from before google_sub: a stale duplicate and its
	// re-link, and bob's with the same name.
	if _, err := db.Exec(`INSERT INTO privatetokens
			(access_token, refresh_token, display_name, client_key, created_on, scope, expires_in, token_type, user_id)
		VALUES ('at', 'rt', 'jyo****ri@example.com', 'stale', now(), '', 0, 'Bearer', $1),
		       ('at', 'rt', 'jyo****ri@example.com', 'current', now(), '', 0, 'Bearer', $1),
		       ('at', 'rt', 'jyo****ri@example.com', 'bobs', now(), '', 0, 'Bearer', $2)`,
		alice, bob); err != nil {
		t.Fatal(err)
	}
	// Scans from before scanmetadata.client_key.
	mine := scanOf(t, alice, "jyo****ri@example.com", "", "")
	bobs := scanOf(t, bob, "jyo****ri@example.com", "", "")
	orphan := scanOf(t, alice, "gone****@example.com", "", "")
	local := scanOf(t, alice, "", "", "")
	kept := scanOf(t, alice, "jyo****ri@example.com", "stale", "")

	for run := 1; run <= 2; run++ {
		if err := backfillScanAccounts(); err != nil {
			t.Fatal(err)
		}
		got := map[int]*string{}
		for _, id := range []int{mine, bobs, orphan, local, kept} {
			var key *string
			if err := db.Get(&key, `SELECT client_key FROM scanmetadata WHERE scan_id = $1`, id); err != nil {
				t.Fatal(err)
			}
			got[id] = key
		}
		want := map[int]string{mine: "current", bobs: "bobs", orphan: "", local: "", kept: "stale"}
		for id, wantKey := range want {
			gotKey := ""
			if got[id] != nil {
				gotKey = *got[id]
			}
			if gotKey != wantKey {
				t.Errorf("run %d: scan %d client_key = %q, want %q", run, id, gotKey, wantKey)
			}
		}
	}
}

func TestCanListProjects(t *testing.T) {
	for scope, want := range map[string]bool{
		"https://www.googleapis.com/auth/devstorage.read_only https://www.googleapis.com/auth/cloudplatformprojects.readonly": true,
		"https://www.googleapis.com/auth/cloud-platform.read-only":                                                            true,
		"https://www.googleapis.com/auth/devstorage.read_only":                                                                false,
		"": false,
	} {
		if got := CanListProjects(scope); got != want {
			t.Errorf("CanListProjects(%q) = %v, want %v", scope, got, want)
		}
	}
}
