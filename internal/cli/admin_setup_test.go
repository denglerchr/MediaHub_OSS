package cli_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"mediahub_oss/internal/cli"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
)

type mockAdminRepo struct {
	repo.Repository
	getUserByUsernameFunc func(ctx context.Context, username string) (repo.User, error)
	createUserFunc        func(ctx context.Context, user repo.User) (repo.User, error)
}

func (m *mockAdminRepo) GetUserByUsername(ctx context.Context, username string) (repo.User, error) {
	if m.getUserByUsernameFunc != nil {
		return m.getUserByUsernameFunc(ctx, username)
	}
	return repo.User{}, customerrors.ErrNotFound
}

func (m *mockAdminRepo) CreateUser(ctx context.Context, user repo.User) (repo.User, error) {
	if m.createUserFunc != nil {
		return m.createUserFunc(ctx, user)
	}
	return user, nil
}

func TestEnsureAdminUser_ConcurrentCreationRace(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Initial GetUserByUsername returns ErrNotFound, but CreateUser encounters ErrUserExists
	// because another pod created the admin user in the meantime. The subsequent
	// GetUserByUsername finds the newly created admin user.
	callCount := 0
	mock := &mockAdminRepo{
		getUserByUsernameFunc: func(ctx context.Context, username string) (repo.User, error) {
			callCount++
			if callCount == 1 {
				return repo.User{}, customerrors.ErrNotFound
			}
			return repo.User{Username: "admin", IsAdmin: true}, nil
		},
		createUserFunc: func(ctx context.Context, user repo.User) (repo.User, error) {
			return repo.User{}, customerrors.ErrUserExists
		},
	}

	err := cli.EnsureAdminUser(ctx, mock, logger)
	if err != nil {
		t.Fatalf("expected EnsureAdminUser to succeed on concurrent creation race, got: %v", err)
	}
}

func TestEnsureAdminUser_CreateUserGenericError(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	genericErr := errors.New("db connection failure")
	mock := &mockAdminRepo{
		getUserByUsernameFunc: func(ctx context.Context, username string) (repo.User, error) {
			return repo.User{}, customerrors.ErrNotFound
		},
		createUserFunc: func(ctx context.Context, user repo.User) (repo.User, error) {
			return repo.User{}, genericErr
		},
	}

	err := cli.EnsureAdminUser(ctx, mock, logger)
	if err == nil {
		t.Fatalf("expected EnsureAdminUser to fail on generic CreateUser error, got nil")
	}
}
