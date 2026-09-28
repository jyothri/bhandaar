package db

import "testing"

// A database from before the Picker API loses the Library API's tables.
func TestMigrateDropsPhotosLibraryTables(t *testing.T) {
	useTestDB(t)
	if _, err := db.Exec(agentserverDDL + `;
		CREATE TABLE photosmediaitem (id serial PRIMARY KEY, scan_id INT NOT NULL);
		CREATE TABLE photometadata (id serial PRIMARY KEY,
			photos_media_item_id INT NOT NULL REFERENCES photosmediaitem (id));
		CREATE TABLE videometadata (id serial PRIMARY KEY,
			photos_media_item_id INT NOT NULL REFERENCES photosmediaitem (id))`); err != nil {
		t.Fatal(err)
	}
	if err := migrateDB(); err != nil {
		t.Fatalf("migrateDB: %v", err)
	}
	var left []string
	if err := db.Select(&left, `SELECT table_name FROM information_schema.tables
		WHERE table_schema = current_schema()
			AND table_name IN ('photosmediaitem', 'photometadata', 'videometadata')`); err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Errorf("tables left after migrateDB: %v", left)
	}
}
