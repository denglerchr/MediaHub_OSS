package userhandler

import (
	"errors"
	"mediahub_oss/internal/httpserver/utils"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
	"mediahub_oss/internal/shared/ulid"
	"net/http"
)

func (p DatabasePermission) toAccessGrant() repo.AccessGrant {
	return repo.NewAccessGrant(p.CanView, p.CanCreate, p.CanEdit, p.CanDelete, p.CanAdmin)
}

func mapAccessGrantToPermission(dbID repo.ULID, grant repo.AccessGrant) DatabasePermission {
	return DatabasePermission{
		DatabaseID: dbID.String(),
		CanView:    grant.HasAccess(repo.AccessView),
		CanCreate:  grant.HasAccess(repo.AccessCreate),
		CanEdit:    grant.HasAccess(repo.AccessEdit),
		CanDelete:  grant.HasAccess(repo.AccessDelete),
		CanAdmin:   grant.HasAccess(repo.AccessAdmin),
	}
}

func mapUserPermissionsToDTO(perms []repo.UserPermissions) []DatabasePermission {
	res := make([]DatabasePermission, 0, len(perms))
	for _, rp := range perms {
		res = append(res, mapAccessGrantToPermission(rp.DatabaseID, rp.Roles))
	}
	return res
}

func mapPermissionsMapToDTO(permsMap map[repo.ULID]repo.AccessGrant) []DatabasePermission {
	res := make([]DatabasePermission, 0, len(permsMap))
	for dbID, grant := range permsMap {
		res = append(res, mapAccessGrantToPermission(dbID, grant))
	}
	return res
}

func hasDuplicateDatabaseIDs(perms []DatabasePermission) bool {
	seenDBs := make(map[string]bool, len(perms))
	for _, p := range perms {
		if seenDBs[p.DatabaseID] {
			return true
		}
		seenDBs[p.DatabaseID] = true
	}
	return false
}

func parseUserIDParam(w http.ResponseWriter, r *http.Request) (repo.ULID, bool) {
	userIDStr := r.PathValue("user_ulid")
	if userIDStr == "" {
		utils.RespondWithError(w, http.StatusBadRequest, "Missing required path parameter: user_ulid")
		return repo.ULID{}, false
	}
	userID, err := ulid.Parse(userIDStr)
	if err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, "Invalid user ID format: must be a valid ULID")
		return repo.ULID{}, false
	}
	return userID, true
}

func scopeAuditFields(scope repo.AccessGrant) map[string]any {
	return map[string]any{
		"scope_view":   scope.HasAccess(repo.AccessView),
		"scope_create": scope.HasAccess(repo.AccessCreate),
		"scope_edit":   scope.HasAccess(repo.AccessEdit),
		"scope_delete": scope.HasAccess(repo.AccessDelete),
		"scope_admin":  scope.HasAccess(repo.AccessAdmin),
	}
}

func apiKeyScopeAuditDetails(key repo.APIKey) map[string]any {
	details := scopeAuditFields(key.Scope)
	details["key_id"] = key.ID.String()
	details["key_name"] = key.Name
	details["key_hint"] = key.KeyHint
	return details
}

func (h *UserHandler) getOwnedAPIKeyOrRespond(w http.ResponseWriter, r *http.Request) (repo.APIKey, string, bool) {
	userIDStr := r.PathValue("user_ulid")
	userID, err := ulid.Parse(userIDStr)
	if err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, "Invalid user ID format: must be a valid ULID")
		return repo.APIKey{}, "", false
	}
	keyIDStr := r.PathValue("key_ulid")
	keyID, err := ulid.Parse(keyIDStr)
	if err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, "Invalid key ID format: must be a valid ULID")
		return repo.APIKey{}, "", false
	}

	key, err := h.Repo.GetAPIKeyByID(r.Context(), keyID)
	if err != nil {
		if errors.Is(err, customerrors.ErrNotFound) {
			utils.RespondWithError(w, http.StatusNotFound, "API Key not found")
		} else {
			h.Logger.Error("Failed to retrieve API key", "error", err, "key_id", keyIDStr)
			utils.RespondWithError(w, http.StatusInternalServerError, "Internal server error")
		}
		return repo.APIKey{}, "", false
	}

	if key.UserID != userID {
		utils.RespondWithError(w, http.StatusNotFound, "API Key not found for this user")
		return repo.APIKey{}, "", false
	}

	return key, userID.String(), true
}

func (h *UserHandler) resolveTargetUserOrRespond(w http.ResponseWriter, r *http.Request, ctxUser *repo.User, userIDStr string) (repo.ULID, string, bool) {
	targetID, err := ulid.Parse(userIDStr)
	if err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, "Invalid user ID format: must be a valid ULID")
		return repo.ULID{}, "", false
	}
	if ctxUser.ID == targetID {
		return ctxUser.ID, ctxUser.Username, true
	}

	user, err := h.Repo.GetUserByID(r.Context(), targetID)
	if err != nil {
		if errors.Is(err, customerrors.ErrNotFound) {
			utils.RespondWithError(w, http.StatusNotFound, "User not found")
		} else {
			h.Logger.Error("Failed to retrieve user", "error", err)
			utils.RespondWithError(w, http.StatusInternalServerError, "Internal server error")
		}
		return repo.ULID{}, "", false
	}
	return user.ID, user.Username, true
}
