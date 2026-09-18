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
  "version": "2.0.0-beta.1",
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
    "oidc_redirect_url": "http://localhost:4200/auth/callback"
  },
  "features": {
    "audit_logs": false
  }
}
```

### Error Responses

This public endpoint does not require authentication and is not expected to return client-side validation errors.
