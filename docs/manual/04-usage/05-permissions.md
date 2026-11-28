---
sidebar_position: 5
title: Database Permissions (RBAC)
---

# Database Permissions (RBAC)

MediaHub enforces a granular Role-Based Access Control (RBAC) model split into **Global Roles** and **Database-Level Permissions**.

---

## 🌐 1. Global Role: `IsAdmin`

* **Superuser Flag**: Users with `IsAdmin = true` bypass all database-level checks.
* **Exclusive Admin Operations**:
  * Creating and deleting databases.
  * User creation, deletion, and permission assignment.
  * System-wide audit log access.

---

## 🔒 2. Database-Level Permissions

For non-admin users, access rights are granted explicitly per database:

| Permission Flag | Description | Endpoint Examples |
| :--- | :--- | :--- |
| `can_view` | Read-only access to view **entries, entry metadata, previews/thumbnails, search results, and database stats** for that specific database (does not grant global database creation/deletion rights). | `GET /api/database/:id/entries`, `GET /api/database/:id/entry/:entryId`, `GET .../preview` |
| `can_create` | Permission to upload new media entries or add a preview where none exists in that specific database. | `POST /api/database/:id/entry`, `POST .../entry/:entryId/preview` |
| `can_edit` | Permission to update existing entry metadata, custom fields, or create/replace entry previews in that database. | `PATCH /api/database/:id/entry/:entryId`, `PUT .../entry/:entryId/preview` |
| `can_delete` | Permission to delete media entries or entry previews from that specific database. | `DELETE /api/database/:id/entry/:entryId`, `DELETE .../preview` |
| `can_admin` | Administrative control over that specific database (updating database settings, renaming, defining custom field schemas, manual housekeeping execution). It does *not* grant rights to create/delete databases globally or manage user permissions. | `PUT /api/database/{id}`, `POST /api/database/{id}/field` |

---

## ⚙️ Configuring Permissions in UI

1. Open **User Management** → Click **Edit Permissions** on a user.
2. Check or uncheck individual permission flags (`can_view`, `can_create`, `can_edit`, `can_delete`, `can_admin`) for each database.
3. Click **Save Permissions**.
