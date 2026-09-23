---
sidebar_position: 3
title: System Info Endpoint
---

# System Information Endpoint

Retrieve system configuration, software version, uptime, media conversion capabilities, and feature flags.

---

## `GET /api/info`

Retrieves general information about the backend service. This endpoint does not require authentication.

* **Role Required**: None (Public)
* **Headers**: None required

### Success Response (`200 OK`)

```json
{
  "service_name": "SWCD MediaHub-API",
  "version": "3.2.0",
  "uptime": "2h15m30s",
  "conversion_to": {
    "image": ["image/jpeg", "image/webp"],
    "audio": ["audio/flac", "audio/opus"],
    "video": ["video/mp4", "video/webm"]
  },
  "oidc": {
    "enabled": false,
    "login_page_disabled": false,
    "oidc_issuer_url": "https://keycloak.example.com/realms/mediahub",
    "oidc_client_id": "mediahub-frontend",
    "oidc_redirect_url": "http://localhost:8080/auth/callback"
  },
  "features": {
    "audit_logs": false
  }
}
```

### OIDC Configuration Fields *(New in v3.2)*
* `enabled` (`boolean`): Whether Single Sign-On (OIDC) is active.
* `login_page_disabled` (`boolean`): Whether the local login form is disabled on the frontend. When `true`, visiting `/login?local=1` provides an administrator break-glass bypass to access local credentials.
* `oidc_issuer_url` (`string`): The base URL of the OpenID Connect identity provider.
* `oidc_client_id` (`string`): The client identifier registered with the identity provider.
* `oidc_redirect_url` (`string`): The authorized redirect callback URL (`/auth/callback`).

### Error Responses

This public endpoint does not require authentication and is not expected to return client-side validation errors.
