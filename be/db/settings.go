package db

import (
	"database/sql"
	"errors"
	"fmt"
)

// Each user's settings, from the Settings page: display preferences. A
// user without a row has the defaults.

type Settings struct {
	// Show the Google Cloud Storage tab on the Request and Browse pages.
	// Off by default: not everyone has a Cloud project. It hides nothing
	// else: buckets already scanned stay in Duplicates, Manage data and
	// Request History, and Cloud Storage scans are still accepted.
	ShowGcs bool `db:"show_gcs" json:"show_gcs"`
}

func migrateSettings() error {
	// updated_at is for debugging; nothing reads it.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS user_settings (
		user_id    BIGINT PRIMARY KEY REFERENCES agent_users(id),
		show_gcs   BOOLEAN NOT NULL DEFAULT false,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("failed to add user_settings to the schema: %w", err)
	}
	return nil
}

// GetSettings returns userID's settings, or the defaults.
func GetSettings(userID int64) (Settings, error) {
	var s Settings
	err := db.Get(&s, `SELECT show_gcs FROM user_settings WHERE user_id = $1`, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("failed to read the settings of user %d: %w", userID, err)
	}
	return s, nil
}

// SaveSettings replaces userID's settings.
func SaveSettings(userID int64, s Settings) error {
	if _, err := db.Exec(`INSERT INTO user_settings (user_id, show_gcs) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET show_gcs = EXCLUDED.show_gcs, updated_at = now()`,
		userID, s.ShowGcs); err != nil {
		return fmt.Errorf("failed to save the settings of user %d: %w", userID, err)
	}
	return nil
}
