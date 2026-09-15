package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared"
	"mediahub_oss/internal/shared/customerrors"
	"strings"
	"time"

	"github.com/Masterminds/squirrel"
)

// GetUserByOIDCIdentity finds the internal user linked to the external OIDC (issuer, subject) identity.
func (r *PostgresRepository) GetUserByOIDCIdentity(ctx context.Context, issuer, subject string) (repo.User, error) {
	query, args, err := r.Builder.Select("u.id", "u.username", "u.password_hash", "u.is_admin", "u.account_type").
		From("users u").
		Join("user_oidc_identities i ON u.id = i.user_id").
		Where(squirrel.Eq{"i.issuer": issuer, "i.subject": subject}).
		ToSql()
	if err != nil {
		return repo.User{}, fmt.Errorf("failed to build get user by oidc identity query: %w", err)
	}

	var user repo.User
	var idStr string
	var accountTypeUint uint8
	err = r.DB.QueryRowContext(ctx, query, args...).Scan(&idStr, &user.Username, &user.PasswordHash, &user.IsAdmin, &accountTypeUint)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return repo.User{}, customerrors.ErrNotFound
		}
		return repo.User{}, fmt.Errorf("failed to scan user by oidc identity: %w", err)
	}
	user.ID = repo.ULID(idStr)
	user.AccountType = repo.AccountType(accountTypeUint)

	return user, nil
}

// CreateOIDCUser provisions an OIDC user atomically: resolving username collisions, inserting the user,
// linking the OIDC identity, and cloning default permissions inside a single database transaction.
func (r *PostgresRepository) CreateOIDCUser(ctx context.Context, params repo.CreateOIDCUserParams) (repo.User, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return repo.User{}, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	baseUsername := repo.ExtractUsernameBase(params.PreferredUsername, params.Email)

	// Query colliding usernames within transaction
	query, args, err := r.Builder.Select("username").
		From("users").
		Where(squirrel.Or{
			squirrel.Eq{"username": baseUsername},
			squirrel.Like{"username": baseUsername + "_%"},
		}).
		ToSql()
	if err != nil {
		return repo.User{}, fmt.Errorf("failed to build colliding usernames query: %w", err)
	}

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return repo.User{}, fmt.Errorf("failed to query colliding usernames: %w", err)
	}

	var colliding []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			rows.Close()
			return repo.User{}, fmt.Errorf("failed to scan colliding username: %w", err)
		}
		colliding = append(colliding, u)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return repo.User{}, fmt.Errorf("row error reading colliding usernames: %w", err)
	}

	uniqueUsername := repo.ResolveUniqueUsername(baseUsername, colliding)

	newUser := repo.User{
		ID:           repo.ULID(shared.GenerateULID()),
		Username:     uniqueUsername,
		PasswordHash: "OIDC_ACCOUNT_NO_PASSWORD",
		IsAdmin:      false,
		AccountType:  repo.AccountTypeOIDC,
	}

	insertUserQuery, insertUserArgs, err := r.Builder.Insert("users").
		Columns("id", "username", "password_hash", "is_admin", "account_type").
		Values(newUser.ID.String(), newUser.Username, newUser.PasswordHash, newUser.IsAdmin, uint8(newUser.AccountType)).
		ToSql()
	if err != nil {
		return repo.User{}, fmt.Errorf("failed to build insert user query: %w", err)
	}

	if _, err := tx.ExecContext(ctx, insertUserQuery, insertUserArgs...); err != nil {
		if isPQUniqueViolation(err) {
			return repo.User{}, customerrors.ErrUserExists
		}
		return repo.User{}, fmt.Errorf("failed to insert user: %w", err)
	}

	// Link OIDC identity
	linkQuery, linkArgs, err := r.Builder.Insert("user_oidc_identities").
		Columns("issuer", "subject", "user_id", "created_at").
		Values(params.Issuer, params.Subject, newUser.ID.String(), time.Now().UnixMilli()).
		ToSql()
	if err != nil {
		return repo.User{}, fmt.Errorf("failed to build link oidc identity query: %w", err)
	}
	if _, err := tx.ExecContext(ctx, linkQuery, linkArgs...); err != nil {
		return repo.User{}, fmt.Errorf("failed to link oidc identity: %w", err)
	}

	// Clone permissions if template username is provided
	if strings.TrimSpace(params.DefaultRightsUser) != "" {
		cloneQuery := `INSERT INTO database_permissions (user_id, database_id, can_view, can_create, can_edit, can_delete, can_admin)
			SELECT $1, database_id, can_view, can_create, can_edit, can_delete, can_admin
			FROM database_permissions
			WHERE user_id = (SELECT id FROM users WHERE username = $2)`
		if _, err := tx.ExecContext(ctx, cloneQuery, newUser.ID.String(), strings.TrimSpace(params.DefaultRightsUser)); err != nil {
			return repo.User{}, fmt.Errorf("failed to clone permissions: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return repo.User{}, fmt.Errorf("failed to commit create oidc user transaction: %w", err)
	}

	return newUser, nil
}
