---
sidebar_position: 1
title: Web Interface & Login
---

# Web Interface & Login

MediaHub embeds its Angular single-page Web UI directly into the Go server binary.

---

## 🌐 Accessing the UI

1. Start your MediaHub server (e.g. `./mediahub serve`).
2. Open your web browser and navigate to `http://localhost:8080` (or your configured port/domain).
3. You will be greeted by the MediaHub login screen.

---

## 🔑 Login & First-Run Admin Setup

### 1. Default Admin Credentials
When MediaHub starts up for the first time without an existing database:
* If `--password` or `MEDIAHUB_PASSWORD` was provided, the default `admin` account is created with that password.
* If no password was provided, MediaHub automatically generates a **random 10-character password** and prints it to the console output:
  ```text
  [INFO] Initialized admin user. Password: aB3xK9pL2q
  ```

### 2. Logging In
Enter `admin` as the username along with your admin password on the login screen.

### 3. Resetting the Admin Password
If you forget the admin password, reset it using the `--reset_pw` CLI flag:

```bash
./mediahub serve --reset_pw=true --password "NewSecurePassword123"
```
On startup, MediaHub updates the existing `admin` user's password to `NewSecurePassword123`.

### 4. Single Sign-On (OIDC / SSO) *(New in v3.2)*
When OIDC integration is enabled (`auth.oidc.enabled = true`), the login page presents a **Login via Single Sign-On** button alongside the standard username/password form. 

Clicking this button redirects users to your enterprise Identity Provider (e.g., Keycloak, Authentik, Okta) to complete authentication. Upon first successful login, MediaHub automatically provisions a local user identity matched to the external OIDC subject.

### 5. Enforced SSO & Administrator Break-Glass (`/login?local=1`)
In enterprise environments, administrators can disable local login forms to enforce SSO-only access across the organization by configuring:

```toml
[auth.oidc]
enabled = true
disable_login_page = true
```

* **Standard User Behavior**: Unauthenticated visits to the application automatically direct users to authenticate via the external Identity Provider. Direct visits to `/login` display the SSO login button with local login forms hidden.
* **Administrator Break-Glass Bypass**: If your Identity Provider is down, misconfigured, or unreachable, administrators can bypass the OIDC enforcement by directly navigating to:

  ```
  http://<mediahub-host>:<port>/login?local=1
  ```

  Visiting `/login?local=1` instructs the login page to bypass the disabled state and display the username and password form. Administrators can then sign in with their local administrative credentials (such as the primary `admin` account) to perform emergency maintenance and restore system settings.

---

## 🧭 Navigating the Dashboard

Once logged in, the top navigation header provides access to:
* **Databases**: View all media databases you have permissions to access, view stats (file count, total storage footprint), and open database viewers.
* **User Management** *(Admin only)*: Manage global users, grant database-level permissions, and manage Service Accounts & API keys.
* **Audit Logs** *(Admin only)*: View detailed access logs (if audit logging is enabled).
* **Settings & Logout**: Manage individual profile settings or end your session.
