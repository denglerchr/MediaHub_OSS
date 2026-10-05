package cli

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math/big"

	"mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"

	"github.com/spf13/viper"
	"golang.org/x/crypto/bcrypt"
)

// EnsureAdminUser verifies the existence of the 'admin' user and handles creation or password resets.
func EnsureAdminUser(ctx context.Context, repo repository.Repository, logger *slog.Logger) error {
	adminUsername := "admin"

	// Viper automatically resolves these from flags (e.g., --password) or environment variables (e.g., MEDIAHUB_PASSWORD)
	passwordFlag := viper.GetString("password")
	resetFlag := viper.GetBool("reset_pw")

	// 1. Check for 'admin' user
	user, err := repo.GetUserByUsername(ctx, adminUsername)
	if err != nil && !errors.Is(err, customerrors.ErrNotFound) {
		return fmt.Errorf("failed to query admin user: %w", err)
	}

	adminExists := (err == nil)

	if !adminExists {
		// Case 1: 'admin' user does NOT exist (First Run)
		pw := passwordFlag
		isRandom := false

		// Generate a random 10-character string if no password is provided
		if pw == "" {
			pw, err = generateRandomPassword(10)
			if err != nil {
				return fmt.Errorf("failed to generate random password: %w", err)
			}
			isRandom = true
		}

		// Hash the password
		hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("failed to hash admin password: %w", err)
		}

		// Create the new admin user
		newUser := repository.User{
			Username:     adminUsername,
			IsAdmin:      true,
			PasswordHash: string(hash),
		}

		_, err = repo.CreateUser(ctx, newUser)
		if err != nil {
			return fmt.Errorf("failed to create admin user: %w", err)
		}

		if isRandom {
			// Print the randomly generated password to the console prominently
			fmt.Printf("\n========================================================\n")
			fmt.Printf(" INITIAL SETUP: An 'admin' user was created.\n")
			fmt.Printf(" Generated Password: %s\n", pw)
			fmt.Printf(" Please log in and change this password immediately.\n")
			fmt.Printf("========================================================\n\n")
			logger.Info("Admin user created with a randomly generated password.")
		} else {
			logger.Info("Admin user created with the provided password.")
		}

	} else {
		// Case 2: 'admin' user exists
		if resetFlag {
			if passwordFlag == "" {
				// Require a password via --password or env var; exit with error if missing
				return fmt.Errorf("cannot reset admin password: the --password flag (or MEDIAHUB_PASSWORD) must be provided when --reset_pw is true")
			}

			// Hash the new password
			hash, err := bcrypt.GenerateFromPassword([]byte(passwordFlag), bcrypt.DefaultCost)
			if err != nil {
				return fmt.Errorf("failed to hash new admin password: %w", err)
			}

			// Update the existing admin password
			user.PasswordHash = string(hash)
			_, err = repo.UpdateUser(ctx, user)
			if err != nil {
				return fmt.Errorf("failed to update admin password: %w", err)
			}

			// The password changed, so revoke any refresh tokens issued under the old
			// password. This runs before the server starts; log and continue on error.
			if err := repo.DeleteAllRefreshTokensForUser(ctx, user.ID); err != nil {
				logger.Error("Failed to revoke admin refresh tokens after password reset", "error", err)
			}

			logger.Info("Admin password has been successfully reset.")
		}
	}

	return nil
}

// generateRandomPassword creates a cryptographically secure random string of a given length.
func generateRandomPassword(length int) (string, error) {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!?@#$%^&*"
	result := make([]byte, length)
	for i := range result {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			return "", err
		}
		result[i] = chars[num.Int64()]
	}
	return string(result), nil
}
