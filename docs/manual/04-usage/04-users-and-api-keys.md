---
sidebar_position: 4
title: Users, Service Accounts & API Keys
---

# Users, Service Accounts & API Keys

MediaHub provides user authentication and API key management for automated background ingestion, edge scripts, and integrations.

---

## 👤 Creating Users & Service Accounts

Administrators can manage users via the **User Management** section in the Web UI:

1. Click **Users** in the header navigation (Admin only).
2. Click **Create User**.
3. Specify credentials:
   * **Username**: Account identifier.
   * **Password**: User password (optional for pure API service accounts).
   * **Is Admin**: Superuser flag (grants unrestricted access across all databases).

### Account Types *(New in v3.2)*
MediaHub classifies accounts into three distinct types:
* **Local Accounts (`local`)**: Created and authenticated directly in MediaHub using internal username/password pairs.
* **Service Accounts (`service_account`)**: Dedicated accounts for machine ingestion and scripts; passwords are not used, and authentication relies strictly on scoped API keys.
* **Single Sign-On Accounts (`oidc`)**: Accounts provisioned automatically when users log in via an external OpenID Connect provider. Permissions can be assigned to OIDC users just like local users.

---

## 🔑 Generating API Keys for Automated Scripts

API Keys allow external scripts, camera edge devices, or automated pipelines to authenticate without sharing account passwords.

API keys can be created in **two ways**:

### Option A: User Self-Service (Account Settings)
Any logged-in user can generate API keys for their own account:
1. Click your username / profile avatar in the top-right header menu.
2. Select **Account Settings** (or Profile).
3. Navigate to the **API Keys** section and click **Create API Key**.
4. Specify a key label (e.g. `Laptop Script`, `Pi Camera 1`) and select scope permissions.
5. Copy and securely store the generated API key token (starts with `srv_`).

### Option B: Administrator Management (User Management)
Administrators can create and manage API keys for any user or service account:
1. Open **User Management** → Select a specific User or Service Account.
2. Click the **API Keys** tab → Click **Create API Key**.
3. Set key description and scope restrictions.

---

## 🛡️ Scope Restrictions

When generating an API Key, you can restrict its capabilities using **Scopes**:

* `scope_view`: Read-only access to entries, metadata, and database details.
* `scope_create`: Permission to upload new media entries.
* `scope_edit`: Permission to update existing entries or metadata.
* `scope_delete`: Permission to delete entries.
* `scope_admin`: Database management operations.

> **Security Intersection Principle**: Effective API key permissions are computed as the **intersection** of the owner user's database permissions and the key's granted scopes. For example, if an API key has `scope_view` only, it cannot create or delete entries even if the owner account has full write/delete rights on that database.
