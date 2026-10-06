---
sidebar_position: 5
title: API Key Endpoints
---

# API Key Management Endpoints

Endpoints for managing stateful API keys linked to user accounts or service accounts for automated background processes and integrations.

Effective API key capabilities are calculated as the **intersection** of the owner user's database permissions and the key's enabled scopes (`scope_view`, `scope_create`, `scope_edit`, `scope_delete`, `scope_admin`).

---

## `GET /api/users/keys`

Retrieves a list of all active API keys in the system across all user accounts.

* **Role Required**: `IsAdmin`
* **Response (`200 OK`)**:
  ```json
  [
    {
      "id": "01J2A3X9D4B5C6E7F8G9H0J1K2",
      "user_id": "01K3B4Y0E5C6D7F8G9H0J1K2L3",
      "name": "backup_script",
      "key_hint": "srv_...a1b2",
      "scope_view": true,
      "scope_create": false,
      "scope_edit": false,
      "scope_delete": false,
      "scope_admin": false,
      "created_at": 1783400000000,
      "expires_at": 1814900000000,
      "last_used_at": 1783450000000,
      "user": {
        "id": "01K3B4Y0E5C6D7F8G9H0J1K2L3",
        "username": "backup_service_account",
        "is_admin": false,
        "account_type": "service_account"
      }
    }
  ]
  ```

---

## `POST /api/user/{user_id}/keys`

Creates a new API key for the specified user account.

* **Role Required**: `IsAdmin` or owner of `{user_id}`
* **Path Parameters**:
  * `user_id` (string, required): ULID of target user.
* **Request Body**:
  ```json
  {
    "name": "backup_script",
    "expires_at": 1814900000000,
    "scope_view": true,
    "scope_create": true,
    "scope_edit": false,
    "scope_delete": false,
    "scope_admin": false
  }
  ```
  *(Note: `expires_at` is a UNIX epoch timestamp in milliseconds. If omitted, the key does not expire).*
* **Response (`201 Created`)**:
  ```json
  {
    "id": "01J2A3X9D4B5C6E7F8G9H0J1K2",
    "user_id": "01K3B4Y0E5C6D7F8G9H0J1K2L3",
    "name": "backup_script",
    "key_hint": "srv_...a1b2",
    "token": "srv_6b89f8c68c12a4b872b22ad716d9a1b2",
    "scope_view": true,
    "scope_create": true,
    "scope_edit": false,
    "scope_delete": false,
    "scope_admin": false,
    "created_at": 1783400000000,
    "expires_at": 1814900000000,
    "last_used_at": null
  }
  ```
> **Important**: The full plaintext `token` string is returned **ONLY ONCE** upon key creation. Store it securely.

---

## `GET /api/user/{user_id}/keys`

Retrieves the list of API keys generated for a specific user account.

* **Role Required**: `IsAdmin` or owner of `{user_id}`
* **Path Parameters**:
  * `user_id` (string, required): ULID of target user.
* **Response (`200 OK`)**:
  ```json
  [
    {
      "id": "01J2A3X9D4B5C6E7F8G9H0J1K2",
      "user_id": "01K3B4Y0E5C6D7F8G9H0J1K2L3",
      "name": "backup_script",
      "key_hint": "srv_...a1b2",
      "scope_view": true,
      "scope_create": true,
      "scope_edit": false,
      "scope_delete": false,
      "scope_admin": false,
      "created_at": 1783400000000,
      "expires_at": 1814900000000,
      "last_used_at": 1783450000000
    }
  ]
  ```

---

## `GET /api/user/{user_id}/keys/{key_ulid}`

Retrieves details of a specific API key.

* **Role Required**: `IsAdmin` or owner of `{user_id}`
* **Path Parameters**:
  * `user_id` (string, required): ULID of target user.
  * `key_ulid` (string, required): ULID of the API key.
* **Response (`200 OK`)**: Returns key metadata object.

---

## `PATCH /api/user/{user_id}/keys/{key_ulid}`

Updates an existing API key's name, expiry timestamp, or scope flags.

* **Role Required**: `IsAdmin` or owner of `{user_id}`
* **Path Parameters**:
  * `user_id` (string, required): ULID of target user.
  * `key_ulid` (string, required): ULID of the API key.
* **Request Body**:
  ```json
  {
    "name": "updated_backup_script",
    "expires_at": 1846400000000,
    "scope_delete": true
  }
  ```
* **Response (`200 OK`)**: Returns updated key metadata object.

---

## `DELETE /api/user/{user_id}/keys/{key_ulid}`

Deletes/revokes an API key.

* **Role Required**: `IsAdmin` or owner of `{user_id}`
* **Path Parameters**:
  * `user_id` (string, required): ULID of target user.
  * `key_ulid` (string, required): ULID of the API key.
* **Response (`200 OK`)**:
  ```json
  {
    "message": "API key 'backup_script' (ID: 01J2A3X9D4B5C6E7F8G9H0J1K2) was successfully deleted."
  }
  ```
