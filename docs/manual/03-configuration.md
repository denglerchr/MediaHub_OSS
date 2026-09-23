---
sidebar_position: 3
title: Configuration
---

# Configuration Reference

MediaHub evaluates configuration using a strict precedence hierarchy:

```
Command-Line Flags  >  Environment Variables  >  config.toml File
```

Any setting provided as a CLI flag takes precedence over environment variables, which take precedence over values in `config.toml`.

---

## ⚙️ Full Configuration Options

| CLI Flag | Environment Variable | Config TOML Key | Description | Default |
| :--- | :--- | :--- | :--- | :--- |
| **Operational Flags** | | | *(Not present in `config.toml`)* | |
| `--config_path` | `MEDIAHUB_CONFIG_PATH` | N/A | Path to the base TOML configuration file | `config.toml` |
| `--init_config` | `MEDIAHUB_INIT_CONFIG` | N/A | Path to TOML initialization config file | `""` |
| `--password` | `MEDIAHUB_PASSWORD` | N/A | Password for the initial `admin` user | `""` |
| `--reset_pw` | `MEDIAHUB_RESET_PW` | N/A | Resets `admin` password to `--password` value | `false` |
| **Server Settings** | | `[server]` | | |
| `--server-host` | `MEDIAHUB_SERVER_HOST` | `host` | Bind host address | `0.0.0.0` |
| `--server-port` | `MEDIAHUB_SERVER_PORT` | `port` | HTTP listening port | `8080` |
| `--server-basepath` | `MEDIAHUB_SERVER_BASEPATH` | `basepath` | Base HTTP path (behind reverse proxy) | `/` |
| `--server-max-sync-upload`| `MEDIAHUB_SERVER_MAX_SYNC_UPLOAD`| `max_sync_upload_size` | RAM threshold for uploads (e.g. `8MB`). Larger files use disk. | `4MB` |
| `--server-cors-origins` | `MEDIAHUB_SERVER_CORS_ORIGINS` | `cors_allowed_origins` | Comma-separated list of allowed CORS origins | `""` |
| **Database Settings** | | `[database]` | | |
| `--database-driver` | `MEDIAHUB_DATABASE_DRIVER` | `driver` | Database driver (`sqlite` or `postgres`) | `sqlite` |
| `--database-source` | `MEDIAHUB_DATABASE_SOURCE` | `source` | SQLite filepath or Postgres connection string | `mediahub.db` |
| `--database-max-open-conns`| `MEDIAHUB_DATABASE_MAX_OPEN_CONNS`| `max_open_conns` | Max open connections (Postgres) | `25` |
| `--database-max-idle-conns`| `MEDIAHUB_DATABASE_MAX_IDLE_CONNS`| `max_idle_conns` | Max idle connections (Postgres) | `25` |
| **Storage Settings** | | `[storage]` | | |
| `--storage-type` | `MEDIAHUB_STORAGE_TYPE` | `type` | Storage provider (`local` or `s3`) | `local` |
| `--storage-local-root` | `MEDIAHUB_STORAGE_LOCAL_ROOT` | `storage.local.root` | Directory path for local file storage | `storage_root` |
| `--storage-s3-endpoint` | `MEDIAHUB_STORAGE_S3_ENDPOINT` | `storage.s3.endpoint` | S3 API endpoint URL | `""` |
| `--storage-s3-region` | `MEDIAHUB_STORAGE_S3_REGION` | `storage.s3.region` | S3 region | `""` |
| `--storage-s3-bucket` | `MEDIAHUB_STORAGE_S3_BUCKET` | `storage.s3.bucket` | S3 bucket name | `""` |
| `--storage-s3-access-key` | `MEDIAHUB_STORAGE_S3_ACCESS_KEY` | `storage.s3.access_key` | S3 access key ID | `""` |
| `--storage-s3-secret-key` | `MEDIAHUB_STORAGE_S3_SECRET_KEY` | `storage.s3.secret_key` | S3 secret access key | `""` |
| `--storage-s3-use-ssl` | `MEDIAHUB_STORAGE_S3_USE_SSL` | `storage.s3.use_ssl` | Enable HTTPS for S3 connection | `true` |
| **Logging Settings** | | `[logging]` | | |
| `--logging-level` | `MEDIAHUB_LOGGING_LEVEL` | `level` | Log level (`debug`, `info`, `warn`, `error`) | `info` |
| `--logging-audit-type` | `MEDIAHUB_LOGGING_AUDIT_TYPE` | `logging.audit.type` | Audit destination (`stdio` or `database`) | `stdio` |
| `--logging-audit-enabled` | `MEDIAHUB_LOGGING_AUDIT_ENABLED` | `logging.audit.enabled` | Enable audit log recording | `false` |
| `--logging-audit-retention`| `MEDIAHUB_LOGGING_AUDIT_RETENTION`| `logging.audit.retention`| Retention duration for database audit logs ("0" to retain indefinitely) | `31d` |
| **Media Settings** | | `[media]` | | |
| `--media-ffmpeg-path` | `MEDIAHUB_MEDIA_FFMPEG_PATH` | `ffmpeg_path` | Custom path to FFmpeg binary | `""` |
| `--media-ffprobe-path` | `MEDIAHUB_MEDIA_FFPROBE_PATH` | `ffprobe_path` | Custom path to FFprobe binary | `""` |
| **Auth Settings (JWT)** | | `[auth.jwt]` | | |
| `--auth-jwt-access-duration`| `MEDIAHUB_AUTH_JWT_ACCESS_DURATION`| `access_duration`| JWT access token validity duration | `"5min"` |
| `--auth-jwt-refresh-duration`| `MEDIAHUB_AUTH_JWT_REFRESH_DURATION`| `refresh_duration`| JWT refresh token validity duration | `"24h"` |
| `--auth-jwt-secret` | `MEDIAHUB_AUTH_JWT_SECRET` | `secret` | Signing secret (auto-generated if empty) | `""` |
| **Auth Settings (OIDC / SSO)** | | `[auth.oidc]` | *(New in v3.2)* | |
| `--auth-oidc-enabled` | `MEDIAHUB_AUTH_OIDC_ENABLED` | `enabled` | Enable OpenID Connect (OIDC / SSO) integration | `false` |
| `--auth-oidc-issuer-url` | `MEDIAHUB_AUTH_OIDC_ISSUER_URL` | `issuer_url` | OIDC Issuer URL (e.g. `https://keycloak.example.com/realms/mediahub`) | `""` |
| `--auth-oidc-client-id` | `MEDIAHUB_AUTH_OIDC_CLIENT_ID` | `client_id` | OIDC Client ID registered with the Identity Provider | `""` |
| `--auth-oidc-client-secret` | `MEDIAHUB_AUTH_OIDC_CLIENT_SECRET` | `client_secret` | OIDC Client Secret | `""` |
| `--auth-oidc-redirect-url` | `MEDIAHUB_AUTH_OIDC_REDIRECT_URL` | `redirect_url` | OIDC redirect callback URL (must end in `/auth/callback`). Defaults to `<origin>/auth/callback` if omitted. | `""` |
| `--auth-oidc-default-user-rights` | `MEDIAHUB_AUTH_OIDC_DEFAULT_USER_RIGHTS` | `default_user_rights` | Default permission role assigned to auto-provisioned OIDC users | `"_oidc_user"` |
| `--auth-oidc-disable-login-page` | `MEDIAHUB_AUTH_OIDC_DISABLE_LOGIN_PAGE` | `disable_login_page` | Disables the local login form and directs users to OIDC. Access `/login?local=1` to bypass and display the form. | `false` |

---

## 📄 Base Configuration Example (`config.toml`)

```toml
[server]
host = "0.0.0.0"
port = 8080
basepath = "/"
max_sync_upload_size = "8MB"

[database]
driver = "sqlite"
source = "mediahub.db"

[storage]
type = "local"

[storage.local]
root = "storage_root"

[logging]
level = "info"

[logging.audit]
type = "stdio"
enabled = false
retention = "31d" # Set to "0" (or "0d") to disable cleanup and retain indefinitely

[media]
ffmpeg_path = ""
ffprobe_path = ""

[auth.jwt]
access_duration = "5min"
refresh_duration = "24h"
secret = ""

# OpenID Connect (SSO) configuration (New in v3.2)
[auth.oidc]
enabled = false
disable_login_page = false # If true, hides local form and directs to SSO. Use /login?local=1 for admin break-glass login
issuer_url = "https://keycloak.example.com/realms/mediahub"
client_id = "mediahub"
client_secret = "your-client-secret"
redirect_url = "" # Optional; defaults to <origin>/auth/callback
default_user_rights = "_oidc_user"
```

:::tip Admin Break-Glass Access (`/login?local=1`)
When `disable_login_page = true` is configured, MediaHub disables the standard username/password login form and forwards users to the external OIDC identity provider.

If the OIDC identity provider becomes unavailable or misconfigured, administrators can bypass the OIDC enforcement by navigating directly to `/login?local=1`. This reveals the local username/password form, allowing local administrators (such as the default `admin` user) to log in and manage the server.
:::

---

## 🚀 One-Time Initialization (`init_config`)

The `--init_config` flag (or `MEDIAHUB_INIT_CONFIG` environment variable) allows you to pass a dedicated TOML file to automatically seed users, roles, database definitions, housekeeping rules, and custom metadata fields on server startup.

### Key Characteristics of `init_config`:
* **Non-destructive**: Will **not** overwrite users or databases if they already exist in the database.
* **Auto-Redaction**: Upon successful execution, MediaHub automatically attempts to overwrite the `init_config` file to redact plaintext `password` fields for security.

### Example `init_config.toml`:

```toml
# --- Users & Permissions ---

[[user]]
name = "AdminUser"
is_admin = true
password = "SuperSecretPassword"

[[user]]
name = "SensorBot"
is_admin = false
password = "BotPassword123"

    [[user.permissions]]
    database_name = "CameraDB"
    can_view = true
    can_create = true
    can_edit = false
    can_delete = false
    can_admin = false

# --- Databases ---

[[database]]
name = "CameraDB"
content_type = "image"
config = { create_preview = true, auto_conversion = "jpeg" }
housekeeping = { interval = "1h", disk_space = "100G", max_age = "0" }
custom_fields = [
    {name = "latitude", type = "REAL"},
    {name = "longitude", type = "REAL"},
    {name = "device_id", type = "TEXT"}
]
```
