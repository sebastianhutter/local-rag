package db

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
)

// Migrate runs any pending schema migrations.
func Migrate(db *sql.DB, embeddingDim int) error {
	var versionStr string
	err := db.QueryRow("SELECT value FROM meta WHERE key = 'schema_version'").Scan(&versionStr)
	if err == sql.ErrNoRows {
		// Fresh database — InitSchema will handle it.
		return InitSchema(db, embeddingDim)
	}
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	current, err := strconv.Atoi(versionStr)
	if err != nil {
		return fmt.Errorf("parse schema version %q: %w", versionStr, err)
	}

	if current >= SchemaVersion {
		slog.Debug("database schema is up to date", "version", current)
		return nil
	}

	if current < 2 {
		// v2: Reclassify git repo collections from 'project' to 'code'.
		if _, err := db.Exec(
			"UPDATE collections SET collection_type = 'code' " +
				"WHERE collection_type = 'project' AND description LIKE 'git-%'",
		); err != nil {
			return fmt.Errorf("migration v2: %w", err)
		}
		slog.Info("migration v2: reclassified git repo collections as 'code'")
	}

	if current < 3 {
		// v3: Add paths column to collections.
		if _, err := db.Exec("ALTER TABLE collections ADD COLUMN paths TEXT"); err != nil {
			// Column may already exist if InitSchema ran first.
			slog.Debug("migration v3: paths column may already exist", "err", err)
		} else {
			slog.Info("migration v3: added paths column to collections")
		}
	}

	if _, err := db.Exec(
		"UPDATE meta SET value = ? WHERE key = 'schema_version'",
		fmt.Sprintf("%d", SchemaVersion),
	); err != nil {
		return fmt.Errorf("update schema version: %w", err)
	}

	slog.Info("database migrated", "from", current, "to", SchemaVersion)
	return nil
}
