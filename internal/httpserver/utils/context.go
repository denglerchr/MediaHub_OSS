package utils

import (
	"context"
	"mediahub_oss/internal/repository"
)

// ContextKey is a custom type for context keys to avoid collisions.
type ContextKey string

const (
	UserKey             ContextKey = "user"
	PermissionHolderKey ContextKey = "permholder"
	APIKeyKey           ContextKey = "apikey"
)

// GetAPIKeyFromContext retrieves the APIKey if the request was authenticated via an API key.
func GetAPIKeyFromContext(ctx context.Context) (*repository.APIKey, bool) {
	val := ctx.Value(APIKeyKey)
	if val == nil {
		return nil, false
	}
	key, ok := val.(*repository.APIKey)
	return key, ok
}

// GetUserFromContext is a helper to safely retrieve the strongly-typed User object.
// Panics if the user is missing, enforcing the guarantee that this is only used on authorized routes.
func GetUserFromContext(ctx context.Context) *repository.User {
	val := ctx.Value(UserKey)
	if val == nil {
		panic("user missing from context")
	}
	return val.(*repository.User)
}

// GetPermissionHolderFromContext is a helper to safely retrieve the user permissions map from the context.
// Panics if the permission holder is missing, enforcing the guarantee that this is only used on authorized routes.
func GetPermissionHolderFromContext(ctx context.Context) PermissionHolder {
	val := ctx.Value(PermissionHolderKey)
	if val == nil {
		panic("permission holder missing from context")
	}
	return val.(PermissionHolder)
}
