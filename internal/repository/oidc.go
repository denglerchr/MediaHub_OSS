package repository

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// MaxUsernameLength is the maximum permitted length for a username in characters/bytes.
const MaxUsernameLength = 64

var likePatternEscaper = strings.NewReplacer(
	`\`, `\\`,
	`%`, `\%`,
	`_`, `\_`,
)

// EscapeLikePattern escapes SQL LIKE wildcard characters ('\', '%', '_') with '\'.
func EscapeLikePattern(s string) string {
	return likePatternEscaper.Replace(s)
}

// TruncateUsername truncates s to at most maxLen bytes and runes on a valid UTF-8 boundary.
func TruncateUsername(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(s) <= maxLen && utf8.RuneCountInString(s) <= maxLen {
		return s
	}
	var end int
	runes := 0
	for i := range s {
		if runes >= maxLen {
			break
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		if i+size > maxLen {
			break
		}
		end = i + size
		runes++
	}
	return s[:end]
}

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
		return TruncateUsername(s, MaxUsernameLength)
	}
	if s := strings.TrimSpace(email); s != "" {
		parts := strings.SplitN(s, "@", 2)
		if len(parts) > 0 && strings.TrimSpace(parts[0]) != "" {
			return TruncateUsername(strings.TrimSpace(parts[0]), MaxUsernameLength)
		}
	}
	return "user"
}

// ResolveUniqueUsername determines a non-colliding username from a candidate base and existing names.
func ResolveUniqueUsername(baseUsername string, existingUsernames []string) string {
	base := TruncateUsername(baseUsername, MaxUsernameLength)
	if !slices.Contains(existingUsernames, base) {
		return base
	}

	i := 2
	for {
		suffix := fmt.Sprintf("_%d", i)
		prefixMax := MaxUsernameLength - len(suffix)
		var candidate string
		if prefixMax > 0 {
			candidate = TruncateUsername(base, prefixMax) + suffix
		} else {
			candidate = TruncateUsername(suffix, MaxUsernameLength)
		}
		if !slices.Contains(existingUsernames, candidate) {
			return candidate
		}
		i++
	}
}
