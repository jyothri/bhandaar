package db

import (
	"database/sql"
	"errors"
	"fmt"
)

// Each user's settings, from the Settings page. A user without a row has
// the defaults.

type Settings struct {
	// Show Google Cloud Storage on the Request and Browse pages. Off by
	// default: not everyone has a Cloud project.
	GcsEnabled bool `db:"gcs_enabled" json:"gcs_enabled"`
}

func migrateSettings() error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS user_settings (
		user_id     BIGINT PRIMARY KEY REFERENCES agent_users(id),
		gcs_enabled BOOLEAN NOT NULL DEFAULT false,
		updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("failed to add user_settings to the schema: %w", err)
	}
	return nil
}

// GetSettings returns userID's settings, or the defaults.
func GetSettings(userID int64) (Settings, error) {
	var s Settings
	err := db.Get(&s, `SELECT gcs_enabled FROM user_settings WHERE user_id = $1`, userID)
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
	if _, err := db.Exec(`INSERT INTO user_settings (user_id, gcs_enabled) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET gcs_enabled = EXCLUDED.gcs_enabled, updated_at = now()`,
		userID, s.GcsEnabled); err != nil {
		return fmt.Errorf("failed to save the settings of user %d: %w", userID, err)
	}
	return nil
}
