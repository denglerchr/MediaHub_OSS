package repository

import (
	"fmt"
	"slices"
	"strings"
)

// CreateOIDCUserParams contains all parameters required to atomically provision an OIDC user.
type CreateOIDCUserParams struct {
	Issuer            string
	Subject           string
	PreferredUsername string
	Email             string
	DefaultRightsUser string // source username whose permissions to clone (optional)
}

// ExtractUsernameBase derives a candidate username from preferred_username, email, or a fallback.
func ExtractUsernameBase(preferredUsername, email string) string {
	if s := strings.TrimSpace(preferredUsername); s != "" {
		return s
	}
	if s := strings.TrimSpace(email); s != "" {
		parts := strings.SplitN(s, "@", 2)
		if len(parts) > 0 && strings.TrimSpace(parts[0]) != "" {
			return strings.TrimSpace(parts[0])
		}
	}
	return "user"
}

// ResolveUniqueUsername determines a non-colliding username from a candidate base and existing names.
func ResolveUniqueUsername(baseUsername string, existingUsernames []string) string {
	if !slices.Contains(existingUsernames, baseUsername) {
		return baseUsername
	}

	i := 2
	for {
		candidate := fmt.Sprintf("%s_%d", baseUsername, i)
		if !slices.Contains(existingUsernames, candidate) {
			return candidate
		}
		i++
	}
}
