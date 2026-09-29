package userhandler

import (
	"encoding/json"
	"errors"
	"mediahub_oss/internal/httpserver/utils"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared"
	"mediahub_oss/internal/shared/customerrors"
	"net/http"

	"golang.org/x/crypto/bcrypt"
)

// GetMe godoc
// @Summary      Retrieve current user
// @Description  Retrieves the user record and their specific permissions for the currently authenticated user. Admins will receive an empty permissions array.
// @Tags         User
// @Produce      json
// @Security     BasicAuth
// @Security     BearerAuth
// @Success      200  {object}  UserResponse
// @Failure      401  {object}  utils.ErrorResponse "Authentication failed"
// @Router       /me [get]
func (h *UserHandler) GetMe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 1. Extract the authenticated user from the request context
	user := utils.GetUserFromContext(ctx)

	holder := utils.GetPermissionHolderFromContext(ctx)
	isAdmin := holder.IsGlobalAdmin()

	// 2. Initialize the base response
	response := UserResponse{
		ID:          user.ID,
		Username:    user.Username,
		IsAdmin:     isAdmin,
		AccountType: user.AccountType,
		Permissions: []DatabasePermission{}, // Default to empty array
	}

	// 3. If the user is an admin, they bypass specific permission checks
	if isAdmin {
		utils.RespondWithJSON(w, http.StatusOK, response)
		return
	}

	// 4. Retrieve permissions from request context cache
	permsMap, err := holder.GetAllPermissions(r.Context())
	if err != nil {
		utils.RespondWithError(w, http.StatusInternalServerError, "Failed to retrieve user permissions")
		return
	}

	// 5. Parse the roles
	for dbID, rp := range permsMap {
		canView := rp.HasAccess(repo.AccessView)
		canCreate := rp.HasAccess(repo.AccessCreate)
		canEdit := rp.HasAccess(repo.AccessEdit)
		canDelete := rp.HasAccess(repo.AccessDelete)
		canAdmin := rp.HasAccess(repo.AccessAdmin)

		response.Permissions = append(response.Permissions, DatabasePermission{
			DatabaseID: dbID.String(),
			CanView:    canView,
			CanCreate:  canCreate,
			CanEdit:    canEdit,
			CanDelete:  canDelete,
			CanAdmin:   canAdmin,
		})
	}

	// 6. Return the populated response
	utils.RespondWithJSON(w, http.StatusOK, response)
}

// UpdateMe godoc
// @Summary      Update current user password
// @Description  Allows the currently authenticated user to update their own password by verifying their old password first.
// @Tags         User
// @Accept       json
// @Produce      json
// @Security     BasicAuth
// @Security     BearerAuth
// @Param        body body userhandler.UpdateMePayload true "Password update payload"
// @Success      200  {object}  utils.MessageResponse "Password updated successfully"
// @Failure      400  {object}  utils.ErrorResponse "Invalid JSON body or missing password fields"
// @Failure      401  {object}  utils.ErrorResponse "Authentication failed: invalid old password"
// @Router       /me [patch]
// @Router       /me [put]
// @Router       /user/me [patch]
// @Router       /user/me [put]
func (h *UserHandler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 1. Extract the authenticated user from the request context
	user := utils.GetUserFromContext(ctx)

	// Single Sign-On users and service accounts cannot change local passwords
	if user.IsOIDC() || user.IsServiceAccount() {
		utils.RespondWithError(w, http.StatusBadRequest, "Password management is disabled for Single Sign-On and Service accounts")
		return
	}

	// 2. Parse the request payload
	var payload UpdateMePayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	// 3. Validate that both fields are provided
	if payload.OldPassword == "" || payload.NewPassword == "" {
		utils.RespondWithError(w, http.StatusBadRequest, "Both old_password and new_password are required")
		return
	}

	// 4. Verify the old password matches the current hash in the database
	err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(payload.OldPassword))
	if err != nil {
		h.Logger.Warn("Failed password update attempt (invalid old password)", "username", user.Username)
		// We return 401 Unauthorized for invalid credentials as per the concept document
		utils.RespondWithError(w, http.StatusUnauthorized, "Authentication failed: invalid old password")
		return
	}

	// 5. Hash the new password securely
	newHash, err := bcrypt.GenerateFromPassword([]byte(payload.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		h.Logger.Error("Failed to hash new password", "error", err)
		utils.RespondWithError(w, http.StatusInternalServerError, "Internal server error")
		return
	}

	// 6. Update the user object and save it using the repository
	user.PasswordHash = string(newHash)

	// Since GetUserFromContext returns a pointer (*repository.User), we dereference it for the Repo call
	_, err = h.Repo.UpdateUser(ctx, *user)
	if err != nil {
		h.Logger.Error("Failed to update user in database", "error", err, "user_id", user.ID)
		utils.RespondWithError(w, http.StatusInternalServerError, "Failed to update password")
		return
	}

	// 7. Log the action in the audit log
	h.Auditor.Log(ctx, "user.update_password", user.Username, "self", nil)

	// 8. Respond with a success message
	utils.RespondWithJSON(w, http.StatusOK, utils.MessageResponse{
		Message: "Password updated successfully.",
	})
}

// GetUsers godoc
// @Summary      Retrieve all users
// @Description  Retrieves a list of all user accounts and their assigned database permissions. Requires the global IsAdmin role.
// @Tags         User
// @Produce      json
// @Security     BasicAuth
// @Security     BearerAuth
// @Success      200  {array}   userhandler.UserResponse "List of users"
// @Failure      401  {object}  utils.ErrorResponse "Authentication failed"
// @Failure      403  {object}  utils.ErrorResponse "Forbidden: User lacks IsAdmin role"
// @Router       /users [get]
func (h *UserHandler) GetUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 1. Parse optional account_type filter
	accountTypeStr := r.URL.Query().Get("account_type")
	var accountTypeFilter *repo.AccountType
	if accountTypeStr != "" {
		val, err := repo.ParseAccountType(accountTypeStr)
		if err != nil {
			utils.RespondWithError(w, http.StatusBadRequest, "Invalid account_type query parameter: must be 'local', 'service_account', or 'oidc'")
			return
		}
		accountTypeFilter = &val
	}

	// 2. Fetch all users from the database
	dbUsers, err := h.Repo.GetUsers(ctx, accountTypeFilter)
	if err != nil {
		h.Logger.Error("Failed to retrieve users", "error", err)
		utils.RespondWithError(w, http.StatusInternalServerError, "Failed to retrieve user list")
		return
	}

	// 3. Initialize our response array
	var response = make([]UserResponse, len(dbUsers))

	// 4. Iterate through each user to build their specific response object
	for i, u := range dbUsers {
		userRes := UserResponse{
			ID:          u.ID,
			Username:    u.Username,
			IsAdmin:     u.IsAdmin,
			AccountType: u.AccountType,
			Permissions: []DatabasePermission{}, // Default to empty
		}

		// 4. Admin users implicitly have all rights, so we leave their permissions array empty
		if !u.IsAdmin {
			rawPerms, err := h.Repo.GetAllUserPermissions(ctx, u.ID)
			if err != nil {
				h.Logger.Warn("Failed to fetch permissions for user", "user_id", u.ID, "error", err)
				// We log the warning but continue so one broken user doesn't crash the whole list
				continue
			}

			// 5. Parse the comma-separated roles into boolean flags
			for _, rp := range rawPerms {
				userRes.Permissions = append(userRes.Permissions, DatabasePermission{
					DatabaseID: rp.DatabaseID.String(), // Updated to DatabaseID
					CanView:    rp.Roles.HasAccess(repo.AccessView),
					CanCreate:  rp.Roles.HasAccess(repo.AccessCreate),
					CanEdit:    rp.Roles.HasAccess(repo.AccessEdit),
					CanDelete:  rp.Roles.HasAccess(repo.AccessDelete),
					CanAdmin:   rp.Roles.HasAccess(repo.AccessAdmin),
				})
			}
		}

		// Add the populated user response to our list
		response[i] = userRes
	}

	// 6. Return the full list
	utils.RespondWithJSON(w, http.StatusOK, response)
}

// CreateUser godoc
// @Summary      Create a new user
// @Description  Creates a new user account and optionally defines their database permissions. Requires the global IsAdmin role.
// @Tags         User
// @Accept       json
// @Produce      json
// @Security     BasicAuth
// @Security     BearerAuth
// @Param        payload body userhandler.CreateUserPayload true "New user details and permissions"
// @Success      201  {object}  userhandler.UserResponse "User successfully created"
// @Failure      400  {object}  utils.ErrorResponse "Invalid JSON body, missing fields, or duplicate database names"
// @Failure      401  {object}  utils.ErrorResponse "Authentication failed"
// @Failure      403  {object}  utils.ErrorResponse "Forbidden: Admin user not retrieved"
// @Failure      409  {object}  utils.ErrorResponse "User already exists"
// @Failure      500  {object}  utils.ErrorResponse "Internal server error"
// @Router       /user [post]
func (h *UserHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	adminUser := utils.GetUserFromContext(ctx)

	// 1. Decode Payload
	var payload CreateUserPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		h.Logger.Warn("Failed to decode CreateUser payload", "error", err)
		utils.RespondWithError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	// 2. Validate basic requirements
	if payload.Username == "" {
		utils.RespondWithError(w, http.StatusBadRequest, "Username is required")
		return
	}

	if payload.AccountType == repo.AccountTypeOIDC {
		utils.RespondWithError(w, http.StatusBadRequest, "OIDC accounts cannot be created directly; they are provisioned automatically via Single Sign-On")
		return
	}

	if payload.AccountType != repo.AccountTypeService && payload.Password == "" {
		utils.RespondWithError(w, http.StatusBadRequest, "Password is required for local user accounts")
		return
	}

	// Validate for duplicate database permissions
	seenDBs := make(map[string]bool)
	for _, p := range payload.Permissions {
		if seenDBs[p.DatabaseID] {
			utils.RespondWithError(w, http.StatusBadRequest, "Duplicate database IDs in permissions list")
			return
		}
		seenDBs[p.DatabaseID] = true
	}

	// 3. Determine password hash
	var passwordHash string
	if payload.AccountType == repo.AccountTypeService {
		passwordHash = "SERVICE_ACCOUNT_NO_LOGIN"
	} else {
		hashBytes, err := bcrypt.GenerateFromPassword([]byte(payload.Password), bcrypt.DefaultCost)
		if err != nil {
			h.Logger.Error("Failed to hash password", "error", err)
			utils.RespondWithError(w, http.StatusInternalServerError, "Internal server error")
			return
		}
		passwordHash = string(hashBytes)
	}

	// 4. Create User in Repository
	newUser := repo.User{
		Username:     payload.Username,
		PasswordHash: passwordHash,
		IsAdmin:      payload.IsAdmin,
		AccountType:  payload.AccountType,
	}

	createdUser, err := h.Repo.CreateUser(ctx, newUser)
	if err != nil {
		// A simple check for a unique constraint violation.
		if errors.Is(err, customerrors.ErrUserExists) {
			utils.RespondWithError(w, http.StatusConflict, "User already exists")
		} else {
			utils.RespondWithError(w, http.StatusInternalServerError, "Failed to create user")
		}
		h.Logger.Error("Failed to create user in database", "error", err)
		return
	}

	// 5. Handle Database Permissions
	var appliedPermissions = []DatabasePermission{}

	if len(payload.Permissions) > 0 {
		for _, perm := range payload.Permissions {
			access := repo.NewAccessGrant(perm.CanView, perm.CanCreate, perm.CanEdit, perm.CanDelete, perm.CanAdmin)

			// Only save if at least one role is assigned
			if access != 0 {
				repoPerm := repo.UserPermissions{
					UserID:     createdUser.ID,
					DatabaseID: repo.ULID(perm.DatabaseID), // Updated to map DatabaseID
					Roles:      access,
				}

				if err := h.Repo.SetUserPermissions(ctx, repoPerm); err != nil {
					h.Logger.Error("Failed to set user permissions", "error", err, "user_id", createdUser.ID, "database_id", perm.DatabaseID) // Updated log
					// We continue rather than fail the whole request, as the user was successfully created
				} else {
					appliedPermissions = append(appliedPermissions, perm)
				}
			}
		}
	}

	// 6. Build the response
	response := UserResponse{
		ID:          createdUser.ID,
		Username:    createdUser.Username,
		IsAdmin:     createdUser.IsAdmin,
		AccountType: createdUser.AccountType,
		Permissions: appliedPermissions,
	}

	// 7. Log the action
	h.Auditor.Log(ctx, "user.create", adminUser.Username, createdUser.Username, map[string]any{
		"is_admin":     createdUser.IsAdmin,
		"account_type": createdUser.AccountType.String(),
	})

	// 8. Return Success
	utils.RespondWithJSON(w, http.StatusCreated, response)
}

// UpdateUser godoc
// @Summary      Update an existing user
// @Description  Updates an existing user's global status, password, or database permissions. Permissions act as an Upsert/Replace operation. Requires the global IsAdmin role.
// @Tags         User
// @Accept       json
// @Produce      json
// @Security     BasicAuth
// @Security     BearerAuth
// @Param        user_ulid path   string true "User ULID"
// @Param        payload   body   userhandler.UpdateUserPayload true "Fields to update"
// @Success      200       {object} userhandler.UserResponse "User successfully updated"
// @Failure      400       {object} utils.ErrorResponse "Missing ID, duplicate DBs, or invalid JSON body"
// @Failure      401       {object} utils.ErrorResponse "Authentication failed"
// @Failure      403       {object} utils.ErrorResponse "Forbidden: Admin user not retrieved"
// @Failure      404       {object} utils.ErrorResponse "User not found"
// @Failure      409       {object} utils.ErrorResponse "Cannot remove last admin user"
// @Failure      500       {object} utils.ErrorResponse "Internal server error"
// @Router       /user/{user_ulid} [patch]
func (h *UserHandler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	adminUser := utils.GetUserFromContext(ctx)

	// 1. Extract the user ID from the path parameters
	userIDStr := r.PathValue("user_ulid")
	if userIDStr == "" {
		utils.RespondWithError(w, http.StatusBadRequest, "Missing required path parameter: user_ulid")
		return
	}
	userID := repo.ULID(userIDStr)
	if !shared.IsValidULID(userIDStr) {
		utils.RespondWithError(w, http.StatusBadRequest, "Invalid user ID format: must be a valid ULID")
		return
	}

	// 2. Decode the payload
	var payload UpdateUserPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		h.Logger.Warn("Failed to decode UpdateUser payload", "error", err)
		utils.RespondWithError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	// Validate for duplicate database permissions
	seenDBs := make(map[string]bool)
	for _, p := range payload.Permissions {
		if seenDBs[p.DatabaseID] {
			utils.RespondWithError(w, http.StatusBadRequest, "Duplicate database IDs in permissions list")
			return
		}
		seenDBs[p.DatabaseID] = true
	}

	// 3. Fetch the existing user
	existingUser, err := h.Repo.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, customerrors.ErrNotFound) {
			utils.RespondWithError(w, http.StatusNotFound, "User not found")
		} else {
			h.Logger.Error("Failed to retrieve user from the database", "error", err, "user_id", userID)
			utils.RespondWithError(w, http.StatusInternalServerError, "Could not retrieve user from the repository.")
		}
		return
	}

	// 4. Update core user fields if provided
	userChanged := false

	if payload.Username != "" && payload.Username != existingUser.Username {
		existingUser.Username = payload.Username
		userChanged = true
	}

	if payload.Password != "" {
		if existingUser.IsOIDC() || existingUser.IsServiceAccount() {
			utils.RespondWithError(w, http.StatusBadRequest, "Cannot set or change password for Single Sign-On or Service accounts")
			return
		}
		hashBytes, err := bcrypt.GenerateFromPassword([]byte(payload.Password), bcrypt.DefaultCost)
		if err != nil {
			utils.RespondWithError(w, http.StatusInternalServerError, "Failed to hash password")
			return
		}
		existingUser.PasswordHash = string(hashBytes)
		userChanged = true
	}

	if payload.IsAdmin != nil {
		// make sure we dont remove the last admin user
		if existingUser.IsAdmin && !(*payload.IsAdmin) {
			adminCount, err := h.Repo.CountAdminUsers(ctx)
			if err != nil {
				h.Logger.Error("Failed to count admin users", "error", err)
				utils.RespondWithError(w, http.StatusInternalServerError, "Repository error while making sure we dont remove last admin user")
				return
			}
			if adminCount <= 1 {
				utils.RespondWithError(w, http.StatusConflict, "Cannot remove last admin user")
				return
			}
		}
		existingUser.IsAdmin = *payload.IsAdmin
		userChanged = true
	}

	if userChanged {
		if _, err := h.Repo.UpdateUser(ctx, existingUser); err != nil {
			h.Logger.Error("Failed to update user record", "error", err, "user_id", userID)
			utils.RespondWithError(w, http.StatusInternalServerError, "Failed to update user")
			return
		}
	}

	// 5. Process permission updates (Upsert or Delete)
	if len(payload.Permissions) > 0 {
		for _, perm := range payload.Permissions {

			access := repo.NewAccessGrant(perm.CanView, perm.CanCreate, perm.CanEdit, perm.CanDelete, perm.CanAdmin)

			repoPerm := repo.UserPermissions{
				UserID:     userID,
				DatabaseID: repo.ULID(perm.DatabaseID), // Updated to use DatabaseID
				Roles:      access,
			}

			if err := h.Repo.SetUserPermissions(ctx, repoPerm); err != nil {
				h.Logger.Error("Failed to update user permission", "error", err, "user_id", userID, "database_id", perm.DatabaseID) // Updated log
			}
		}
	}

	// 6. Construct the final response object to return to the client
	finalPermissions := []DatabasePermission{}
	if !existingUser.IsAdmin {
		rawPerms, err := h.Repo.GetAllUserPermissions(ctx, existingUser.ID)
		if err == nil {
			for _, rp := range rawPerms {
				finalPermissions = append(finalPermissions, DatabasePermission{
					DatabaseID: rp.DatabaseID.String(),
					CanView:    rp.Roles.HasAccess(repo.AccessView),
					CanCreate:  rp.Roles.HasAccess(repo.AccessCreate),
					CanEdit:    rp.Roles.HasAccess(repo.AccessEdit),
					CanDelete:  rp.Roles.HasAccess(repo.AccessDelete),
					CanAdmin:   rp.Roles.HasAccess(repo.AccessAdmin),
				})
			}
		}
	}

	response := UserResponse{
		ID:          existingUser.ID,
		Username:    existingUser.Username,
		IsAdmin:     existingUser.IsAdmin,
		AccountType: existingUser.AccountType,
		Permissions: finalPermissions,
	}

	// 7. Log the action
	h.Auditor.Log(ctx, "user.update", adminUser.Username, existingUser.Username, nil)

	utils.RespondWithJSON(w, http.StatusOK, response)
}

// DeleteUser godoc
// @Summary      Delete a user
// @Description  Deletes a user account and cascades the deletion to remove their database permissions. Requires the global IsAdmin role.
// @Tags         User
// @Produce      json
// @Security     BasicAuth
// @Security     BearerAuth
// @Param        id  query    int true "User ID"
// @Success      200 {object} utils.MessageResponse "User successfully deleted"
// @Failure      400 {object} utils.ErrorResponse "Missing or invalid ID format"
// @Failure      401 {object} utils.ErrorResponse "Authentication failed"
// @Failure      403 {object} utils.ErrorResponse "Forbidden: Admin user not retrieved"
// @Failure      404 {object} utils.ErrorResponse "User not found"
// @Failure      409 {object} utils.ErrorResponse "Cannot delete the last remaining admin user"
// @Failure      500 {object} utils.ErrorResponse "Internal server error"
// @Router       /user [delete]
func (h *UserHandler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Extract the admin user performing the action for the audit log
	adminUser := utils.GetUserFromContext(ctx)

	// 1. Extract the user ID from the path parameters
	userIDStr := r.PathValue("user_ulid")
	if userIDStr == "" {
		utils.RespondWithError(w, http.StatusBadRequest, "Missing required path parameter: user_ulid")
		return
	}
	userID := repo.ULID(userIDStr)
	if !shared.IsValidULID(userIDStr) {
		utils.RespondWithError(w, http.StatusBadRequest, "Invalid user ID format: must be a valid ULID")
		return
	}

	// 2. Fetch the existing user to verify they exist and get their username/admin status
	userToDelete, err := h.Repo.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, customerrors.ErrNotFound) {
			utils.RespondWithError(w, http.StatusNotFound, "User not found")
		} else {
			h.Logger.Error("Failed to fetch user for deletion", "error", err, "user_id", userID)
			utils.RespondWithError(w, http.StatusInternalServerError, "Failed to retrieve user")
		}
		return
	}

	// 3. Prevent deletion of the last remaining admin user
	if userToDelete.IsAdmin {
		adminCount, err := h.Repo.CountAdminUsers(ctx)
		if err != nil {
			h.Logger.Error("Failed to count admin users", "error", err)
			utils.RespondWithError(w, http.StatusInternalServerError, "Repository error while checking admin count")
			return
		}

		if adminCount <= 1 {
			utils.RespondWithError(w, http.StatusConflict, "Cannot delete the last remaining admin user")
			return
		}
	}

	// 4. Delete the user
	if err := h.Repo.DeleteUser(ctx, userID); err != nil {
		if errors.Is(err, customerrors.ErrNotFound) {
			utils.RespondWithError(w, http.StatusNotFound, "User not found")
		} else {
			h.Logger.Error("Failed to delete user", "error", err, "user_id", userID)
			utils.RespondWithError(w, http.StatusInternalServerError, "Failed to delete user account")
		}
		return
	}

	// 5. Audit log the deletion
	h.Auditor.Log(ctx, "user.delete", adminUser.Username, userToDelete.Username, map[string]any{
		"deleted_id": userID.String(),
	})

	// 6. Return success message matching the concept document
	utils.RespondWithJSON(w, http.StatusOK, utils.MessageResponse{
		Message: "User '" + userToDelete.Username + "' (ID: " + userID.String() + ") was successfully deleted.",
	})
}

// GetUser godoc
// @Summary      Retrieve a user
// @Description  Retrieves a specific user account and their database permissions. Requires the global IsAdmin role.
// @Tags         User
// @Produce      json
// @Security     BasicAuth
// @Security     BearerAuth
// @Param        user_ulid path string true "User ULID"
// @Success      200 {object} userhandler.UserResponse "User record"
// @Failure      400 {object} utils.ErrorResponse "Missing or invalid user ULID"
// @Failure      401 {object} utils.ErrorResponse "Authentication failed"
// @Failure      403 {object} utils.ErrorResponse "Forbidden: Admin user not retrieved"
// @Failure      404 {object} utils.ErrorResponse "User not found"
// @Router       /user/{user_ulid} [get]
func (h *UserHandler) GetUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	adminUser := utils.GetUserFromContext(ctx)

	userIDStr := r.PathValue("user_ulid")
	if userIDStr == "" {
		utils.RespondWithError(w, http.StatusBadRequest, "Missing required path parameter: user_ulid")
		return
	}
	userID := repo.ULID(userIDStr)
	if !shared.IsValidULID(userIDStr) {
		utils.RespondWithError(w, http.StatusBadRequest, "Invalid user ID format: must be a valid ULID")
		return
	}

	user, err := h.Repo.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, customerrors.ErrNotFound) {
			utils.RespondWithError(w, http.StatusNotFound, "User not found")
		} else {
			h.Logger.Error("Failed to retrieve user", "error", err, "user_id", userID)
			utils.RespondWithError(w, http.StatusInternalServerError, "Internal server error")
		}
		return
	}

	finalPermissions := []DatabasePermission{}
	if !user.IsAdmin {
		rawPerms, err := h.Repo.GetAllUserPermissions(ctx, user.ID)
		if err == nil {
			for _, rp := range rawPerms {
				finalPermissions = append(finalPermissions, DatabasePermission{
					DatabaseID: rp.DatabaseID.String(),
					CanView:    rp.Roles.HasAccess(repo.AccessView),
					CanCreate:  rp.Roles.HasAccess(repo.AccessCreate),
					CanEdit:    rp.Roles.HasAccess(repo.AccessEdit),
					CanDelete:  rp.Roles.HasAccess(repo.AccessDelete),
					CanAdmin:   rp.Roles.HasAccess(repo.AccessAdmin),
				})
			}
		}
	}

	response := UserResponse{
		ID:          user.ID,
		Username:    user.Username,
		IsAdmin:     user.IsAdmin,
		AccountType: user.AccountType,
		Permissions: finalPermissions,
	}

	h.Auditor.Log(ctx, "user.get", adminUser.Username, user.Username, map[string]any{
		"user_id":      string(user.ID),
		"is_admin":     user.IsAdmin,
		"account_type": user.AccountType.String(),
	})

	utils.RespondWithJSON(w, http.StatusOK, response)
}
