package migrations

import (
	"embed"
	"fmt"
)

//go:embed sqlite/*.sql postgres/*.sql
var EmbedFS embed.FS

// RequiredVersion is the database schema version required by this version of MediaHub.
const RequiredVersion = 3004

// CheckVersion validates if the database schema version matches the expected RequiredVersion.
// If the version does not match, it returns an error with the instructions on how to upgrade or downgrade the database.
func CheckVersion(currentVersion int) error {
	if currentVersion == RequiredVersion {
		return nil
	}

	if currentVersion < RequiredVersion {
		return fmt.Errorf("database schema version (%d) is older than the required version (%d). Please run:\n    mediahub migrate up\nto upgrade your database schema", currentVersion, RequiredVersion)
	}

	return fmt.Errorf("database schema version (%d) is newer than the required version (%d). Please use the newer mediahub version you have been using, or use that newer version to run:\n    mediahub migrate down\nto downgrade your database schema", currentVersion, RequiredVersion)
}
