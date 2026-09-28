package db

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// Linked Google accounts (privatetokens). See docs/archive/request-drive-scans.md,
// "Identity and re-linking".

// Services a linked account can be scanned for, as GET /api/accounts names them.
const (
	ServiceGmail  = "gmail"
	ServiceDrive  = "drive"
	ServicePhotos = "photos"
)

// scopeServices maps each Google scope to the service it allows.
var scopeServices = map[string]string{
	"https://www.googleapis.com/auth/gmail.readonly":          ServiceGmail,
	"https://www.googleapis.com/auth/drive.metadata.readonly": ServiceDrive,
	"https://www.googleapis.com/auth/drive.readonly":          ServiceDrive,
	"https://www.googleapis.com/auth/drive":                   ServiceDrive,
	// Picked items only; the Library API's scopes no longer read a library.
	"https://www.googleapis.com/auth/photospicker.mediaitems.readonly": ServicePhotos,
}

// Services lists the services a granted scope (Google's space-separated
// list) allows, in a fixed order. An empty scope means Gmail: only Gmail
// was linked before scopes were recorded per service.
func Services(scope string) []string {
	if strings.TrimSpace(scope) == "" {
		return []string{ServiceGmail}
	}
	granted := map[string]bool{}
	for _, s := range strings.Fields(scope) {
		if service, ok := scopeServices[s]; ok {
			granted[service] = true
		}
	}
	services := []string{}
	for _, service := range []string{ServiceGmail, ServiceDrive, ServicePhotos} {
		if granted[service] {
			services = append(services, service)
		}
	}
	return services
}

// HasService reports whether a granted scope allows service.
func HasService(scope string, service string) bool {
	for _, s := range Services(scope) {
		if s == service {
			return true
		}
	}
	return false
}

// migrateAccounts adds the Google account ID (the OpenID `sub`) to linked
// accounts, so that linking an account again updates its row; and the
// linked account to scans, so that history groups by account, not by its
// masked name, which two accounts can share.
func migrateAccounts() error {
	statements := []string{
		`ALTER TABLE privatetokens ADD COLUMN IF NOT EXISTS google_sub TEXT`,
		`CREATE UNIQUE INDEX IF NOT EXISTS privatetokens_user_google_sub
			ON privatetokens (user_id, google_sub) WHERE google_sub IS NOT NULL`,
		`ALTER TABLE scanmetadata ADD COLUMN IF NOT EXISTS client_key VARCHAR(100)`,
		`CREATE INDEX IF NOT EXISTS scanmetadata_client_key ON scanmetadata (client_key)`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("failed to add account keys to the schema: %w", err)
		}
	}
	return backfillScanAccounts()
}

// backfillScanAccounts gives scans recorded before scanmetadata.client_key
// the account their name belongs to: the scan owner's newest linked account
// of that name, which is also the one re-linking adopts. Rows with a
// client_key are never touched, so this is safe at every start.
func backfillScanAccounts() error {
	result, err := db.Exec(`UPDATE scanmetadata sm SET client_key = (
			SELECT p.client_key FROM privatetokens p
			WHERE p.user_id = s.user_id AND p.display_name = sm.name
			ORDER BY p.id DESC LIMIT 1)
		FROM scans s
		WHERE s.id = sm.scan_id AND sm.client_key IS NULL AND COALESCE(sm.name, '') <> ''
			AND EXISTS (SELECT 1 FROM privatetokens p
				WHERE p.user_id = s.user_id AND p.display_name = sm.name)`)
	if err != nil {
		return fmt.Errorf("failed to backfill scan accounts: %w", err)
	}
	if n, _ := result.RowsAffected(); n > 0 {
		slog.Info("Recorded the linked account of earlier scans", "scans", n)
	}
	var unmatched int
	if err := db.Get(&unmatched, `SELECT count(*) FROM scanmetadata
		WHERE client_key IS NULL AND COALESCE(name, '') <> ''`); err != nil {
		return fmt.Errorf("failed to count scans without an account: %w", err)
	}
	if unmatched > 0 {
		slog.Warn("Scans whose account name matches no linked account; Request History won't list them",
			"scans", unmatched)
	}
	return nil
}

// GoogleLink is what one account-linking round trip with Google gave.
type GoogleLink struct {
	GoogleSub    string
	DisplayName  string
	AccessToken  string
	RefreshToken string
	Scope        string // as granted
	ExpiresIn    int16
	TokenType    string
}

// LinkAccount stores a Google account userID linked, and returns its
// client_key. The row updated is, in order: the user's row for the same
// Google account; else the user's newest row from before google_sub with
// the same display name, which then gets the sub; else a new row, keyed by
// newClientKey.
func LinkAccount(userID int64, link GoogleLink, newClientKey string) (string, error) {
	if link.GoogleSub == "" {
		return "", errors.New("google account ID is empty")
	}
	tx, err := db.Beginx()
	if err != nil {
		return "", fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	var clientKey string
	err = tx.Get(&clientKey, `SELECT client_key FROM privatetokens
		WHERE user_id = $1 AND google_sub = $2 FOR UPDATE`, userID, link.GoogleSub)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.Get(&clientKey, `SELECT client_key FROM privatetokens
			WHERE user_id = $1 AND google_sub IS NULL AND display_name = $2
			ORDER BY id DESC LIMIT 1 FOR UPDATE`, userID, link.DisplayName)
	}
	switch {
	case err == nil:
		_, err = tx.Exec(`UPDATE privatetokens SET google_sub = $1, display_name = $2,
				access_token = $3, refresh_token = $4, scope = $5, expires_in = $6, token_type = $7
			WHERE client_key = $8`,
			link.GoogleSub, link.DisplayName, link.AccessToken, link.RefreshToken,
			link.Scope, link.ExpiresIn, link.TokenType, clientKey)
	case errors.Is(err, sql.ErrNoRows):
		clientKey = newClientKey
		_, err = tx.Exec(`INSERT INTO privatetokens
				(access_token, refresh_token, display_name, client_key, scope, expires_in, token_type,
				 created_on, user_id, google_sub)
			VALUES ($1, $2, $3, $4, $5, $6, $7, current_timestamp, $8, $9)`,
			link.AccessToken, link.RefreshToken, link.DisplayName, clientKey,
			link.Scope, link.ExpiresIn, link.TokenType, userID, link.GoogleSub)
	}
	if err != nil {
		return "", fmt.Errorf("failed to save linked account: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("failed to save linked account: %w", err)
	}
	return clientKey, nil
}

// GetRequestAccountsFromDb lists the Google accounts userID linked, oldest
// first, with the services each allows.
func GetRequestAccountsFromDb(userID int64) ([]Account, error) {
	rows := []struct {
		ClientKey   string `db:"client_key"`
		DisplayName string `db:"display_name"`
		Scope       string `db:"scope"`
		GoogleSub   string `db:"google_sub"`
	}{}
	err := db.Select(&rows, `SELECT client_key, COALESCE(display_name, '') AS display_name,
			COALESCE(scope, '') AS scope, COALESCE(google_sub, '') AS google_sub
		FROM privatetokens WHERE user_id = $1 ORDER BY id`, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get request accounts: %w", err)
	}
	accounts := make([]Account, len(rows))
	for i, row := range rows {
		accounts[i] = Account{
			ClientKey:   row.ClientKey,
			DisplayName: row.DisplayName,
			Services:    Services(row.Scope),
			LoginHint:   row.GoogleSub,
		}
	}
	return accounts, nil
}

// ScannedAccount is a linked account that has scans, as Request History
// lists it.
type ScannedAccount struct {
	ClientKey   string `db:"client_key" json:"clientKey"`
	DisplayName string `db:"name" json:"displayName"`
}

// GetAccountsFromDb lists the accounts userID has scans of, by name, each
// named by its newest scan.
func GetAccountsFromDb(userID int64) ([]ScannedAccount, error) {
	accounts := []ScannedAccount{}
	err := db.Select(&accounts, `SELECT client_key, name FROM (
			SELECT DISTINCT ON (sm.client_key) sm.client_key, COALESCE(sm.name, '') AS name
			FROM scanmetadata sm JOIN scans s ON s.id = sm.scan_id
			WHERE s.user_id = $1 AND sm.client_key IS NOT NULL
			ORDER BY sm.client_key, sm.scan_id DESC
		) latest ORDER BY name, client_key`, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get accounts: %w", err)
	}
	return accounts, nil
}

// GetScanRequestsFromDb lists userID's scans of the account clientKey,
// newest first.
func GetScanRequestsFromDb(userID int64, clientKey string) ([]ScanRequests, error) {
	if len(strings.TrimSpace(clientKey)) == 0 {
		return []ScanRequests{}, nil
	}
	scanRequests := []ScanRequests{}
	err := db.Select(&scanRequests, `SELECT COALESCE(sm.name, '') AS name,
			COALESCE(sm.search_filter, '') AS search_filter, COALESCE(sm.search_path, '') AS search_path, s.id, s.scan_type, s.scan_start_time,
			COALESCE(EXTRACT(EPOCH FROM (s.scan_end_time - s.scan_start_time)), -1) AS scan_duration_in_sec,
			COALESCE(s.status, 'Completed') AS status
		FROM scans s JOIN scanmetadata sm ON sm.scan_id = s.id
		WHERE sm.client_key = $1 AND s.user_id = $2
		ORDER BY s.id DESC`, clientKey, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get scan requests for account %s: %w", clientKey, err)
	}
	return scanRequests, nil
}
