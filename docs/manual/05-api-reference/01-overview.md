---
sidebar_position: 1
title: Auth & RBAC Overview
---

# Authentication & RBAC Overview

All API endpoints are available under the `/api` route prefix. Responses return standard JSON objects.

---

## 🔐 Hybrid Authentication Model

MediaHub supports three authentication methods:

1. **Basic Authentication**: Send the standard header:
   ```text
   Authorization: Basic <base64(username:password)>
   ```
2. **Bearer Token (JWT)**: Send an access token header:
   ```text
   Authorization: Bearer <access_token>
   ```
3. **API Keys**: Send an API key token header:
   ```text
   Authorization: Bearer srv_<api_key_secret>
   ```

---

## 🛡️ Role-Based Access Control (RBAC)

Permissions are split into **Global Roles** and **Database-Level Permissions**:

### Global Roles
* **`IsAdmin`**: Superuser status. Bypasses database checks and has exclusive access to User Management, global Database Creation/Deletion, and Audit Logs.

### Database-Level Permissions
* `can_view`: Read-only access to view entries, metadata, previews/thumbnails, search results, and database stats for that specific database.
* `can_create`: Permission to upload new media entries or add previews where none exist in that specific database.
* `can_edit`: Permission to update existing entry metadata, custom fields, or create/replace entry previews in that database.
* `can_delete`: Permission to delete media entries or entry previews from that specific database.
* `can_admin`: Permission to update database settings, rename, manage custom fields, and execute manual housekeeping.

---

## ⚠️ Error Response Schema

All error responses (4xx / 5xx) return a standard JSON payload:

```json
{
  "error": "Human-readable error description"
}
```
