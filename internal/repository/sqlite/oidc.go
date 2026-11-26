package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
	"mediahub_oss/internal/shared/ulid"
	"strings"

	"github.com/Masterminds/squirrel"
)

const maxCreateOIDCUserAttempts = 5

type oidcCreateOutcome int

const (
	oidcCreateSuccess oidcCreateOutcome = iota
	oidcCreateRetryUsername
	oidcCreateDuplicateIdentity
)

// GetUserByOIDCIdentity finds the internal user linked to the external OIDC (issuer, subject) identity.
func (r *SQLiteRepository) GetUserByOIDCIdentity(ctx context.Context, issuer, subject string) (repo.User, error) {
	query, args, err := r.Builder.Select("u.id", "u.username", "u.password_hash", "u.is_admin", "u.account_type").
		From("users u").
		Join("user_oidc_identities i ON u.id = i.user_id").
		Where(squirrel.Eq{"i.issuer": issuer, "i.subject": subject}).
		ToSql()
	if err != nil {
		return repo.User{}, fmt.Errorf("failed to build get user by oidc identity query: %w", err)
	}

	var user repo.User
	var accountTypeUint uint8
	err = r.DB.QueryRowContext(ctx, query, args...).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.IsAdmin, &accountTypeUint)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return repo.User{}, customerrors.ErrNotFound
		}
		return repo.User{}, fmt.Errorf("failed to scan user by oidc identity: %w", err)
	}
	user.AccountType = repo.AccountType(accountTypeUint)

	return user, nil
}

// CreateOIDCUser provisions an OIDC user atomically: resolving username collisions, inserting the user,
// linking the OIDC identity, and cloning default permissions inside a single database transaction.
func (r *SQLiteRepository) CreateOIDCUser(ctx context.Context, params repo.CreateOIDCUserParams) (repo.User, error) {
	baseUsername := repo.ExtractUsernameBase(params.PreferredUsername, params.Email)

	for attempt := 0; attempt < maxCreateOIDCUserAttempts; attempt++ {
		user, outcome, err := r.tryCreateOIDCUserAttempt(ctx, params, baseUsername)
		if err != nil {
			return repo.User{}, err
		}
		switch outcome {
		case oidcCreateSuccess:
			return user, nil
		case oidcCreateDuplicateIdentity:
			existing, fetchErr := r.GetUserByOIDCIdentity(ctx, params.Issuer, params.Subject)
			if fetchErr != nil {
				return repo.User{}, fmt.Errorf("failed to fetch existing oidc user after identity conflict: %w", fetchErr)
			}
			return existing, nil
		case oidcCreateRetryUsername:
			continue
		}
	}

	return repo.User{}, customerrors.ErrUserExists
}

func (r *SQLiteRepository) tryCreateOIDCUserAttempt(ctx context.Context, params repo.CreateOIDCUserParams, baseUsername string) (repo.User, oidcCreateOutcome, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return repo.User{}, 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	likeConditions := squirrel.Or{
		squirrel.Eq{"username": baseUsername},
		squirrel.Expr("username LIKE ? ESCAPE '\\'", repo.EscapeLikePattern(baseUsername)+`\_%`),
	}
	if len(baseUsername) > repo.MaxUsernameLength-12 {
		shortPrefix := repo.TruncateUsername(baseUsername, repo.MaxUsernameLength-12)
		likeConditions = append(likeConditions, squirrel.Expr("username LIKE ? ESCAPE '\\'", repo.EscapeLikePattern(shortPrefix)+`%\_%`))
	}

	// Query colliding usernames within transaction
	query, args, err := r.Builder.Select("username").
		From("users").
		Where(likeConditions).
		ToSql()
	if err != nil {
		return repo.User{}, 0, fmt.Errorf("failed to build colliding usernames query: %w", err)
	}

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return repo.User{}, 0, fmt.Errorf("failed to query colliding usernames: %w", err)
	}

	var colliding []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			rows.Close()
			return repo.User{}, 0, fmt.Errorf("failed to scan colliding username: %w", err)
		}
		colliding = append(colliding, u)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return repo.User{}, 0, fmt.Errorf("row error reading colliding usernames: %w", err)
	}

	uniqueUsername := repo.ResolveUniqueUsername(baseUsername, colliding)

	newUser := repo.User{
		ID:           ulid.New(),
		Username:     uniqueUsername,
		PasswordHash: "OIDC_ACCOUNT_NO_PASSWORD",
		IsAdmin:      false,
		AccountType:  repo.AccountTypeOIDC,
	}

	insertUserQuery, insertUserArgs, err := r.Builder.Insert("users").
		Columns("id", "username", "password_hash", "is_admin", "account_type").
		Values(newUser.ID, newUser.Username, newUser.PasswordHash, newUser.IsAdmin, int(newUser.AccountType)).
		ToSql()
	if err != nil {
		return repo.User{}, 0, fmt.Errorf("failed to build insert user query: %w", err)
	}

	if _, err := tx.ExecContext(ctx, insertUserQuery, insertUserArgs...); err != nil {
		if isSQLiteUniqueViolation(err) {
			return repo.User{}, oidcCreateRetryUsername, nil
		}
		return repo.User{}, 0, fmt.Errorf("failed to insert user: %w", err)
	}

	// Link OIDC identity
	linkQuery, linkArgs, err := r.Builder.Insert("user_oidc_identities").
		Columns("issuer", "subject", "user_id", "created_at").
		Values(params.Issuer, params.Subject, newUser.ID, squirrel.Expr("CAST(unixepoch('subsec') * 1000 AS INTEGER)")).
		ToSql()
	if err != nil {
		return repo.User{}, 0, fmt.Errorf("failed to build link oidc identity query: %w", err)
	}
	if _, err := tx.ExecContext(ctx, linkQuery, linkArgs...); err != nil {
		if isSQLiteUniqueViolation(err) {
			return repo.User{}, oidcCreateDuplicateIdentity, nil
		}
		return repo.User{}, 0, fmt.Errorf("failed to link oidc identity: %w", err)
	}

	// Clone permissions if template username is provided
	if strings.TrimSpace(params.DefaultRightsUser) != "" {
		cloneQuery := `INSERT INTO database_permissions (user_id, database_id, can_view, can_create, can_edit, can_delete, can_admin)
			SELECT ?, database_id, can_view, can_create, can_edit, can_delete, can_admin
			FROM database_permissions
			WHERE user_id = (SELECT id FROM users WHERE username = ?)`
		if _, err := tx.ExecContext(ctx, cloneQuery, newUser.ID, strings.TrimSpace(params.DefaultRightsUser)); err != nil {
			return repo.User{}, 0, fmt.Errorf("failed to clone permissions: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return repo.User{}, 0, fmt.Errorf("failed to commit create oidc user transaction: %w", err)
	}

	return newUser, oidcCreateSuccess, nil
}
