package initconfig

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"golang.org/x/crypto/bcrypt"

	"mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
	"mediahub_oss/internal/shared/ulid"
)

const (
	initConfigLockName         = "init_config"
	initConfigLockTTL          = 2 * time.Minute
	initConfigLockWaitTimeout  = 30 * time.Second
	initConfigLockPollInterval = 250 * time.Millisecond
	initConfigUnlockTimeout    = 5 * time.Second
)

func acquireInitConfigLock(ctx context.Context, repo repository.Repository, ownerID string) error {
	deadline := time.NewTimer(initConfigLockWaitTimeout)
	defer deadline.Stop()

	ticker := time.NewTicker(initConfigLockPollInterval)
	defer ticker.Stop()

	for {
		acquired, err := repo.AcquireLock(ctx, initConfigLockName, ownerID, initConfigLockTTL)
		if err != nil {
			return fmt.Errorf("failed to acquire init config lock: %w", err)
		}
		if acquired {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("canceled while waiting for init config lock: %w", ctx.Err())
		case <-deadline.C:
			return fmt.Errorf("timed out waiting for init config lock: %w", customerrors.ErrLockNotAcquired)
		case <-ticker.C:
		}
	}
}

func fetchDatabaseNameMap(ctx context.Context, repo repository.Repository) (map[string]repository.ULID, error) {
	existingDBs, err := repo.GetDatabases(ctx)
	if err != nil {
		return nil, err
	}
	dbNameToID := make(map[string]repository.ULID, len(existingDBs))
	for _, db := range existingDBs {
		dbNameToID[db.Name] = db.ID
	}
	return dbNameToID, nil
}

// Apply executes the initialization configuration against the repository.
func Apply(ctx context.Context, config *InitConfig, repo repository.Repository, logger *slog.Logger, filePath string) error {
	ownerID := fmt.Sprintf("initconfig-%s", ulid.New())
	if err := acquireInitConfigLock(ctx, repo, ownerID); err != nil {
		return err
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), initConfigUnlockTimeout)
		defer cancel()
		released, err := repo.ReleaseLock(unlockCtx, initConfigLockName, ownerID)
		if err != nil {
			logger.Warn("Failed to release init config lock", "owner_id", ownerID, "error", err)
		} else if !released {
			logger.Warn("Init config lock was not released; lock expired or was claimed by another instance", "owner_id", ownerID)
		}
	}()

	// 0. Fetch existing databases AFTER acquiring the distributed lock so the
	// Name -> ID resolution map reflects any pod that initialized just before us.
	dbNameToID, err := fetchDatabaseNameMap(ctx, repo)
	if err != nil {
		return fmt.Errorf("failed to fetch existing databases for init config resolution: %w", err)
	}

	// 1. Initialize Databases
	for _, dbInit := range config.Databases {
		if _, exists := dbNameToID[dbInit.Name]; !exists {
			hk, err := dbInit.GetHousekeeping()
			if err != nil {
				logger.Error("Failed to parse housekeeping config", "database", dbInit.Name, "error", err)
				continue
			}

			customFields := make([]repository.CustomFieldDef, len(dbInit.CustomFields))
			validFields := true
			for i, cf := range dbInit.CustomFields {
				fieldName := strings.TrimSpace(cf.Name)
				if fieldName == "" || repository.IsReservedCustomFieldName(fieldName) {
					logger.Error("Invalid or reserved custom field name in init config", "database", dbInit.Name, "field", cf.Name)
					validFields = false
					break
				}
				fieldType, err := repository.ParseCustomFieldType(cf.Type)
				if err != nil {
					logger.Error("Invalid custom field type in init config", "database", dbInit.Name, "field", cf.Name, "type", cf.Type, "error", err)
					validFields = false
					break
				}
				isIndexed := true
				if cf.IsIndexed != nil {
					isIndexed = *cf.IsIndexed
				}
				customFields[i] = repository.CustomFieldDef{
					ID:        i,
					Name:      fieldName,
					Type:      fieldType,
					IsIndexed: isIndexed,
				}
			}

			if !validFields {
				continue
			}

			db := repository.Database{
				Name:        dbInit.Name,
				ContentType: dbInit.ContentType,
				NMaxQueued:  dbInit.NMaxQueued,
				Config: repository.DatabaseConfig{
					CreatePreview:  dbInit.Config.CreatePreview,
					AutoConversion: dbInit.Config.AutoConversion,
				},
				Housekeeping: hk,
				CustomFields: customFields,
			}

			createdDB, err := repo.CreateDatabase(ctx, db)
			if err != nil {
				if errors.Is(err, customerrors.ErrDatabaseExists) {
					logger.Debug("Database from init config already exists, refreshing ID map", "database", dbInit.Name)
					refreshedMap, refreshErr := fetchDatabaseNameMap(ctx, repo)
					if refreshErr != nil {
						logger.Error("Failed to refresh database map after ErrDatabaseExists", "database", dbInit.Name, "error", refreshErr)
					} else if id, ok := refreshedMap[dbInit.Name]; ok {
						dbNameToID[dbInit.Name] = id
					}
				} else {
					logger.Error("Failed to create init database", "database", dbInit.Name, "error", err)
				}
			} else {
				logger.Info("Created database from init config", "database", dbInit.Name)
				// Add the newly generated ULID to our resolution map so user permissions can use it!
				dbNameToID[createdDB.Name] = createdDB.ID
			}
		} else {
			logger.Debug("Database from init config already exists, skipping", "database", dbInit.Name)
		}
	}

	// 2. Initialize Users
	passwordsRedacted := false
	for i, userInit := range config.Users {
		_, err := repo.GetUserByUsername(ctx, userInit.Name)
		if errors.Is(err, customerrors.ErrNotFound) {
			accountType := repository.AccountTypeLocal
			if userInit.AccountType != "" {
				parsed, err := repository.ParseAccountType(userInit.AccountType)
				if err != nil {
					logger.Error("Invalid account_type for user in init config", "user", userInit.Name, "account_type", userInit.AccountType, "error", err)
					continue
				}
				if parsed == repository.AccountTypeOIDC {
					logger.Error("OIDC accounts cannot be created directly via init config", "user", userInit.Name)
					continue
				}
				accountType = parsed
			}

			var passwordHash string
			if accountType == repository.AccountTypeService {
				if userInit.Password != "" {
					logger.Warn("Password provided for service account in init config will be ignored; service accounts authenticate via API keys", "user", userInit.Name)
				}
				passwordHash = "SERVICE_ACCOUNT_NO_LOGIN"
			} else {
				if userInit.Password == "" {
					logger.Error("Password is required for local user in init config", "user", userInit.Name)
					continue
				}
				hash, err := bcrypt.GenerateFromPassword([]byte(userInit.Password), bcrypt.DefaultCost)
				if err != nil {
					logger.Error("Failed to hash password for user", "user", userInit.Name, "error", err)
					continue
				}
				passwordHash = string(hash)
			}

			user := repository.User{
				Username:     userInit.Name,
				IsAdmin:      userInit.IsAdmin,
				PasswordHash: passwordHash,
				AccountType:  accountType,
			}

			createdUser, err := repo.CreateUser(ctx, user)
			if err != nil {
				logger.Error("Failed to create init user", "user", userInit.Name, "error", err)
				continue
			}
			logger.Info("Created user from init config", "user", userInit.Name)

			// Assign Permissions
			for _, permInit := range userInit.Permissions {
				// Resolve the database name to its ULID
				dbID, ok := dbNameToID[permInit.DatabaseName]
				if !ok {
					logger.Error("Cannot set permission, database not found", "user", userInit.Name, "database_name", permInit.DatabaseName)
					continue
				}

				err := repo.SetUserPermissions(ctx, repository.UserPermissions{
					UserID:     createdUser.ID,
					DatabaseID: dbID, // Use the resolved ULID here!
					Roles:      repository.NewAccessGrant(permInit.CanView, permInit.CanCreate, permInit.CanEdit, permInit.CanDelete, permInit.CanAdmin),
				})

				if err != nil {
					logger.Error("Failed to set permissions for user", "user", userInit.Name, "database", permInit.DatabaseName, "error", err)
				}
			}

		} else if err != nil {
			logger.Error("Error checking user existence", "user", userInit.Name, "error", err)
		} else {
			logger.Debug("User from init config already exists, skipping", "user", userInit.Name)
		}

		// Clear the plaintext password in memory for all users
		if userInit.Password != "" {
			config.Users[i].Password = ""
			passwordsRedacted = true
		}
	}

	// 3. Redact passwords in the TOML file if any passwords were present
	if passwordsRedacted && filePath != "" {
		if err := redactPasswordsInFile(filePath, config); err != nil {
			logger.Warn("Failed to overwrite init config file to remove passwords", "path", filePath, "error", err)
		} else {
			logger.Info("Successfully removed plaintext passwords from init config file", "path", filePath)
		}
	}

	return nil
}

// redactPasswordsInFile encodes the sanitized configuration back to the file system.
func redactPasswordsInFile(filePath string, config *InitConfig) error {
	f, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	encoder := toml.NewEncoder(f)
	return encoder.Encode(config)
}
