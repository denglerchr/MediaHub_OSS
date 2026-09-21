package userhandler

import (
	"log/slog"

	"mediahub_oss/internal/logging/audit"
	"mediahub_oss/internal/repository"
)

type UserHandler struct {
	Logger  *slog.Logger
	Auditor audit.AuditLogger
	Repo    repository.Repository
}

// UpdateMePayload defines the expected JSON body for PATCH /api/me.
type UpdateMePayload struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// CreateUserPayload defines the expected JSON body for POST /api/user.
type CreateUserPayload struct {
	Username    string                 `json:"username"`
	Password    string                 `json:"password"`
	IsAdmin     bool                   `json:"is_admin"`
	AccountType repository.AccountType `json:"account_type"`
	Permissions []DatabasePermission   `json:"permissions"`
}

// UpdateUserPayload defines the expected JSON body for PATCH /api/user.
type UpdateUserPayload struct {
	Username    string               `json:"username"`
	Password    string               `json:"password"`
	IsAdmin     *bool                `json:"is_admin"`
	Permissions []DatabasePermission `json:"permissions"`
}

// UserResponse is the JSON structure returned by the /api/me and /api/users endpoints.
type UserResponse struct {
	ID          repository.ULID         `json:"id"`
	Username    string                  `json:"username"`
	IsAdmin     bool                    `json:"is_admin"`
	AccountType repository.AccountType `json:"account_type"`
	Permissions []DatabasePermission    `json:"permissions"`
}

// DatabasePermission defines the boolean flags for a user's rights on a specific database.
type DatabasePermission struct {
	DatabaseID string `json:"database_id"`
	CanView    bool   `json:"can_view"`
	CanCreate  bool   `json:"can_create"`
	CanEdit    bool   `json:"can_edit"`
	CanDelete  bool   `json:"can_delete"`
	CanAdmin   bool   `json:"can_admin"`
}
