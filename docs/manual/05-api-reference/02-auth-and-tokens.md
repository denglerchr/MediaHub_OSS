---
sidebar_position: 2
title: Auth & Token Endpoints
---

# Authentication & Token Endpoints

Endpoints for acquiring, refreshing, and revoking JSON Web Tokens (JWT).

---

## `POST /api/token`

Obtain an internal JWT Access and Refresh token pair using local HTTP Basic Auth credentials.

* **Role Required**: None (Public)
* **Header**: `Authorization: Basic <base64(username:password)>`
* **Body**: None

### Response (`200 OK`)
```json
{
  "access_token": "eyJhbGciOi...",
  "refresh_token": "eyJhbGciOi..."
}
```

---

## `POST /api/token/oidc` *(New in v3.2)*

Exchange an external OpenID Connect authorization code for an internal JWT Access and Refresh token pair. Automatically provisions a user identity if this is the user's first login.

* **Role Required**: None (Public)
* **Header**: `Content-Type: application/json`
* **Request Body**:
  ```json
  {
    "code": "SplxlOBeZQQYbYS6WxSbIA",
    "redirect_uri": "http://localhost:8080/auth/callback",
    "code_verifier": "optional_pkce_verifier"
  }
  ```

### Response (`200 OK`)
```json
{
  "access_token": "eyJhbGciOi...",
  "refresh_token": "eyJhbGciOi..."
}
```

### Error Responses
* **`400 Bad Request`**: Single Sign-On is disabled on the server or the request body is malformed.
* **`401 Unauthorized`**: Authorization code is invalid, expired, or failed IdP verification.
* **`500 Internal Server Error`**: OIDC provider connection failure or server error.

---

## `POST /api/token/refresh`

Exchange a valid Refresh Token for a new Access/Refresh token pair (Token Rotation).

* **Role Required**: None (Public)
* **Request Body**:
  ```json
  {
    "refresh_token": "eyJhbGciOi..."
  }
  ```
* **Response (`200 OK`)**:
  ```json
  {
    "access_token": "new_access_token...",
    "refresh_token": "new_refresh_token..."
  }
  ```

---

## `POST /api/logout`

Revoke a Refresh Token and end the current user session.

* **Role Required**: Authenticated User
* **Header**: `Authorization: Bearer <access_token>`
* **Request Body**:
  ```json
  {
    "refresh_token": "eyJhbGciOi..."
  }
  ```
* **Response (`200 OK`)**:
  ```json
  {
    "message": "Logged out successfully."
  }
  ```
