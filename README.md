# MediaHub API & Web Interface (v3.1.0) ✨

This open source project provides a HTTP REST API and web frontend for working with images, audio, videos or generic files The software has a dependency on ffmpeg for automatic transcoding of files and metadata extraction.

<p align="center">
  <img src="screenshots/Dataflow2.webp" alt="MediaHub" width="600">
</p>

Intended use-cases for the software are data acquisition pipelines:

  * storing of camera data with metadata in a production environment, e.g., images or videos for product quality checks, wildlife observation etc
  * storing of audio samples captured using microphones, e.g., for condition monitoring of machines, animal observation

You can find screenshots of what the frontend looks like in the [screenshots](/screenshots/) folder. Documentation is available [here](https://mediahub-docs.swcd.lu/).

## Distinction from other tools

There are some other tools for self-hosted image storage and retrieval available, e.g., you might want to check out Immich, PhotoPrism or LibrePhotos, and there might be others. Those tools focus on providing a replacement for cloud solutions like Google Photos, and organize photos and videos in albums. 

Mediahub is not trying to be a self-hosted replacement for Google Photos, but was developed mainly to serve as part of the backend pipeline for automated picture and audio acquisition and processing systems. It is distinct by these two features:

- configurable deletion of old pictures, which is required on edge devices
- rich and indexed metadata management (e.g., results of an ML pipeline)
- auto-conversion on ingestion to target format to keep the database footprint low

In summary, if you need a place to store your private photos, other solutions might have an edge. If you have a camera acquisition system that runs 24/7 and you want to store the captured media, Mediahub might be the tool for you. 

-----

## 🚀 Features

  * **Database & Storage Flexibility:** Supports both **SQLite** and **PostgreSQL** database drivers, as well as **Local Filesystem** and **S3-compatible API** (MinIO, Wasabi, AWS S3, ...) storage solutions.
  * **Database Management:** Create, list, view details, update housekeeping rules, and delete files managed in databases.
  * **Dynamic Metadata:** Supports defining custom fields (e.g., `score`, `source`, `defect`) for each database. These fields are stored and indexed for efficient searching.
  * **Automated Housekeeping:** A background service periodically cleans up files based on configurable age (set to `0` to disable) and disk space limits (set to `0` to disable).
  * **Media Processing:** Configure databases to automatically transcode media files, e.g., images to Webp, video to Webm or audio files to FLAC.
  * **Integrated Web UI:** The Go application serves the Angular frontend from the embedded binary, providing a seamless user experience from a single executable.
  * **Bulk Import & Export:** Export and import your data as zip-files.
  * **Advanced Entry Search:** The API supports powerful filtering on custom fields with operators like `>`, `<`, `>=`, `<=`, `!=`, and `LIKE` (for wildcard text search).
  * **Hybrid Authentication:** Supports both **Basic Authentication** (for simple API scripts) and **JWT (JSON Web Tokens)** with Access/Refresh tokens (for the Web UI), protected by role-based access control.
  * **Flexible User Roles:** User roles can be defined on database level, allowing fine grained access control.
  * **Audit Logging:** Optional logging of every action taken by users can be enabled for traceability. 

-----

## 📥 Downloads

You can download prebuild binaries for different architectures using the provided links.

| Operating System | Architecture | Download Link |
| :--- | :--- | :--- |
| Linux | AArch64 (ARM 64-bit) | [mediahub\_linux\_aarch64](https://downloads.swcd.lu/MediaHub/latest/mediahub_linux_aarch64) |
| Linux | x86_64 (AMD/Intel 64-bit) | [mediahub\_linux\_x86\_64](https://downloads.swcd.lu/MediaHub/latest/mediahub_linux_x86_64) |
| Windows | AArch64 (ARM 64-bit) | [mediahub\_windows\_aarch64.exe](https://downloads.swcd.lu/MediaHub/latest/mediahub_windows_aarch64.exe) |
| Windows | x86_64 (AMD/Intel 64-bit) | [mediahub\_windows\_x86\_64.exe](https://downloads.swcd.lu/MediaHub/latest/mediahub_windows_x86_64.exe) |

A template `config.toml` file is also available for download [here](https://downloads.swcd.lu/MediaHub/latest/config.toml).

As an alternative, you can run a docker container using the [docker image](https://hub.docker.com/r/denglerchr/mediahub_oss) from Dockerhub. Place your config file in a folder `mediahub_config` and run:

```bash
docker run -d \
  --name mediahub \
  -p 8080:8080 \
  -v $(pwd)/mediahub_storage:/storage \
  -e MEDIAHUB_PASSWORD="your-secure-password" \
  denglerchr/mediahub_oss:2.1.0
```

-----

## ▶️ Running the Application

After downloading or building the application, you need a `config.toml` file as well. Simply put it in the same folder, or start the binary with the `--config_path` option.
The binary will create `mediahub.db` and the `storage_root` directory in the same folder where it is run, unless configured otherwise.
You can then visit the web UI under the port you configured.

You can get a short help message with

```bash
./mediahub --help
```

to start the server use

```bash
./mediahub serve
```

### 1\. Admin User Setup

The application automatically manages one or multiple `admin` account with full permissions.

**On First Run:**
If no `admin` user is found in the database, the server will create one.

  * **With Password:** If you provide a password via the `--password` flag or `MEDIAHUB_PASSWORD` environment variable, that password will be used.
    ```bash
    ./mediahub serve --password "my-secure-password"
    ```
  * **Without Password:** If no password is provided, a **random 10-character password** will be generated and **printed to the console**. Use this password to log in.

**Resetting the Admin Password:**
You can reset the `admin` user's password at any time by using the `--reset_pw=true` flag along with a new password.

```bash
# This will update the existing admin's password to "new-pass-123"
./mediahub serve --reset_pw=true --password "new-pass-123"
```

### 2\. Run the Server

From the project's root directory, execute the binary you built earlier:

```bash
# Make sure you are in the project root directory
./mediahub serve
```

The server will start, typically on `http://localhost:8080`.

### 3\. Access the Web Interface

  * Open your web browser and navigate to `http://localhost:8080`.
  * You will be directed to the login page. Use the admin credentials (e.g., `admin` / `my-secure-password`).

-----

## 🛠️ Maintenance Commands

In addition to the web server, the binary includes several maintenance tools accessible via subcommands.

### Database Recovery

If the server crashes during a file upload, some entries may get stuck in a "processing" state. The recovery command scans the database and fixes these inconsistencies.

```bash
# Runs the integrity check and fixes stuck entries (does not start the web server)
./mediahub recovery
```

### Database Migrations

You can manually manage the database schema versions using the `migrate` command. This is useful for upgrading the database structure explicitly. It is strongly advised to do a backup of the database before applying any migration.

```bash
# Check current migration status
./mediahub migrate status

# Apply all pending migrations (Up)
./mediahub migrate up

# Rollback the last migration (Down)
# Use with care and a backup of the database! This can permanently remove data!
./mediahub migrate down
```

-----

## 🔧 Configuration

The application is configured using a hierarchy of settings. Any value set by a **command-line flag** will override a value set by an **environment variable**, which in turn overrides any value set in the **`config.toml` file**.

### 1\. Base Configuration (`config.toml`)

On startup, the application looks for a `config.toml` file in its working directory (or at the path specified by `--config_path`). This file defines the base settings for the server.

**Example `config.toml`:**

```toml
[server]
host = "0.0.0.0"   # The host address to bind to
port = 8080        # Default port (can be overridden by flag/env)
basepath = "/"     # For the case of a reverse proxy
max_sync_upload_size = "8MB" # Threshold for switching from RAM to Disk processing
# cors_allowed_origins = ["http://localhost:4200"]

[database]
# Driver: "sqlite" or "postgres"
driver = "sqlite"
source = "mediahub.db"

[storage]
# Type: "local" or "s3"
type = "local"

[storage.local]
root = "storage_root"

[logging]
level = "info" # Standard application logging level

[logging.audit]
type = "stdio" # Where to store audit logs: "stdio" or "database"
enabled = false # Toggle audit logging on or off
retention = "31d" # how long to store the logs in case of "database"

[media]
ffmpeg_path = ""
ffprobe_path = ""

[auth.jwt]
# Token expiration settings
access_duration = "5min"
refresh_duration = "24h"
# Secret is auto-generated and saved here if missing
secret = "..."
```

### 2\. Flags & Environment Variables (Overrides)

You can override any setting from the `config.toml` file using environment variables or command-line flags. This is the recommended way to configure docker setups.

| Flag | Env Variable | Description | Default |
| --- | --- | --- | --- |
| **Operational & Startup Flags** |  | *(These do not map to `config.toml`)* |  |
| `--config_path` | `MEDIAHUB_CONFIG_PATH` | Path to the base TOML configuration file. | `config.toml` |
| `--init_config` | `MEDIAHUB_INIT_CONFIG` | Path to a TOML config file for one-time initialization of users/databases. | `""` |
| `--password` | `MEDIAHUB_PASSWORD` | The password for the 'admin' user (used on first run or with reset). | `""` |
| `--reset_pw` | `MEDIAHUB_RESET_PW` | If `true`, resets the 'admin' password on startup to the one provided. | `false` |
| **Server Settings** `[server]` |  |  |  |
| `--server-host` | `MEDIAHUB_SERVER_HOST` | The host address to bind to. | `0.0.0.0` |
| `--server-port` | `MEDIAHUB_SERVER_PORT` | The HTTP port to bind to. | `8080` |
| `--server-basepath` | `MEDIAHUB_SERVER_BASEPATH` | The base path in case the app is behind a reverse proxy. | `/` |
| `--server-max-sync-upload` | `MEDIAHUB_SERVER_MAX_SYNC_UPLOAD` | RAM threshold for uploads (e.g., "8MB"). Larger files use disk. | `4MB` |
| `--server-cors-origins` | `MEDIAHUB_SERVER_CORS_ORIGINS` | Comma-separated list of allowed CORS origins. | `""` |
| **Database Settings** `[database]` |  |  |  |
| `--database-driver` | `MEDIAHUB_DATABASE_DRIVER` | Database driver to use (`sqlite` or `postgres`). | `sqlite` |
| `--database-source` | `MEDIAHUB_DATABASE_SOURCE` | Path to DB file or connection string. | `mediahub.db` |
| `--database-max-open-conns` | `MEDIAHUB_DATABASE_MAX_OPEN_CONNS` | Relevant only for PostgreSQL. | `25` |
| `--database-max-idle-conns` | `MEDIAHUB_DATABASE_MAX_IDLE_CONNS` | Relevant only for PostgreSQL. | `25` |
| **Storage Settings** `[storage]` |  |  |  |
| `--storage-type` | `MEDIAHUB_STORAGE_TYPE` | Storage backend type (`local` or `s3`). | `local` |
| `--storage-local-root` | `MEDIAHUB_STORAGE_LOCAL_ROOT` | Root directory for `local` file storage. | `storage_root` |
| `--storage-s3-endpoint` | `MEDIAHUB_STORAGE_S3_ENDPOINT` | S3 API endpoint (e.g., `s3.amazonaws.com`). | `""` |
| `--storage-s3-region` | `MEDIAHUB_STORAGE_S3_REGION` | S3 region. | `""` |
| `--storage-s3-bucket` | `MEDIAHUB_STORAGE_S3_BUCKET` | S3 bucket name. | `""` |
| `--storage-s3-access-key` | `MEDIAHUB_STORAGE_S3_ACCESS_KEY` | S3 Access Key. | `""` |
| `--storage-s3-secret-key` | `MEDIAHUB_STORAGE_S3_SECRET_KEY` | S3 Secret Key. | `""` |
| `--storage-s3-use-ssl` | `MEDIAHUB_STORAGE_S3_USE_SSL` | Enable HTTPS for S3 connection (`true`/`false`). | `true` |
| **Logging Settings** `[logging]` |  |  |  |
| `--logging-level` | `MEDIAHUB_LOGGING_LEVEL` | Application logging verbosity (`debug`, `info`, `warn`, `error`). | `info` |
| `--logging-audit-type` | `MEDIAHUB_LOGGING_AUDIT_TYPE` | Where to store audit logs (`stdio` or `database`). | `stdio` |
| `--logging-audit-enabled` | `MEDIAHUB_LOGGING_AUDIT_ENABLED` | Toggle audit logging (`true`/`false`). | `false` |
| `--logging-audit-retention` | `MEDIAHUB_LOGGING_AUDIT_RETENTION` | In case of logging to database. | `31d` |
| **Media Settings** `[media]` |  |  |  |
| `--media-ffmpeg-path` | `MEDIAHUB_MEDIA_FFMPEG_PATH` | Path to FFmpeg executable. | `""` |
| `--media-ffprobe-path` | `MEDIAHUB_MEDIA_FFPROBE_PATH` | Path to FFprobe executable. | `""` |
| **Auth Settings** `[auth]` |  |  |  |
| `--auth-jwt-access-duration` | `MEDIAHUB_AUTH_JWT_ACCESS_DURATION` | Validity of the JWT. | `"5min"` |
| `--auth-jwt-refresh-duration` | `MEDIAHUB_AUTH_JWT_REFRESH_DURATION` | Validity of the refresh token. | `"24h"` |
| `--auth-jwt-secret` | `MEDIAHUB_AUTH_JWT_SECRET` | Secret key for signing JWTs. | `""` |

### 3\. One-Time Initialization (`--init_config`)

You can provide a *separate* TOML configuration file on startup using the `--init_config` flag or the `MEDIAHUB_INIT_CONFIG` environment variable. The server will read this file and **create any users or databases that do not already exist**. This is useful for automated deployments.

  * This process **will not overwrite** existing users or databases.
  * After a successful run, the server will **attempt to overwrite the init config file** to remove the plaintext `password` fields for security.
  * If this write fails (e.g., due to file permissions), the server will log a warning and continue, but you should **manually secure the file** to remove the passwords.

**Example Init Config (`my-init.toml`):**

```toml
# --- Users ---

# 1. Admin User
[[user]]
name = "AdminUser"
is_admin = true
password = "SuperSecretPassword"

# 2. Regular User with specific permissions
[[user]]
name = "Bob"
is_admin = false
password = "BobsPassword"

    # Bob's permissions for ImageDB1
    [[user.permissions]]
    database_name = "ImageDB1"
    can_view = true
    can_create = true
    can_edit = false
    can_delete = false
    can_admin = false

    # Bob's permissions for Audio_Archive
    [[user.permissions]]
    database_name = "Audio_Archive"
    can_view = true
    can_create = false
    can_edit = false
    can_delete = false
    can_admin = false


# --- Databases ---

[[database]]
name = "ImageDB1"
content_type = "image"
config = { create_preview = true, auto_conversion = "image/jpeg" }
housekeeping = { interval = "1h", disk_space = "100G", max_age = "0" }
# Custom metadata schema
custom_fields = [
    {name = "latitude", type = "REAL"},
    {name = "longitude", type = "REAL"},
    {name = "description", type = "TEXT", is_indexed=false}
]

[[database]]
name = "Audio_Archive"
content_type = "audio"
config = { create_preview = true, auto_conversion = "audio/flac" }
housekeeping = { interval = "24h", disk_space = "500G", max_age = "0" } # Disable age-based cleanup
custom_fields = [
    {name = "source", type = "TEXT"}
]

[[database]]
name = "MyMovies"
content_type = "video"
config = { create_preview = true, auto_conversion = "video/mp4" }
housekeeping = { interval = "24h", disk_space = "1T", max_age = "730d" }
custom_fields = [
    {name = "director", type = "TEXT"},
    {name = "rating", type = "INTEGER"}
]

```


-----

## 🛠️ Prerequisites for Building

As mentioned you can just use prebuild binaries, but if you want to build the program yourself, you can will need the following.

  * **Go:** Version 1.25 or later (as specified in `go.mod`).
  * **Node.js & npm:** Required for building the frontend.
  * **Angular CLI:** The command-line interface for Angular. Install globally with `npm install -g @angular/cli`.
  * **FFmpeg & FFprobe:** Required for transcoding, preview generation and metadata extraction.

-----

## ⚙️ Building the Application

The build process compiles the Go backend with the Angular frontend embedded directly into the final executable. All commands should be run from the **root directory** of the project.

### 1\. (Optional) Generate API Documentation

If you have made changes to the API handlers, regenerate the Swagger documentation *before* building the frontend.

```bash
# Ensure you have the swag CLI tool installed
# go install github.com/swaggo/swag/cmd/swag@latest

# From the project root, regenerate the docs
swag init -g ./cmd/mediahub/main.go
```

The generated documentation is served at the `/swagger/index.html` endpoint.

### 2\. Build the Frontend (Angular UI)

This step builds the static Angular application and places the output files where the Go backend can find and embed them.

```bash
# Navigate into the frontend directory
cd frontend

# Install Node.js dependencies
npm install

# Build the static assets for production
npm run build

# Go back to the root directory
cd ..
```

The `angular.json` file is configured to output the build to `cmd/mediahub/frontend_embed/`. This location is crucial for the Go compiler to find and embed the files.

### 3\. Build the Backend (Go API)

This final step compiles the Go application, embeds the frontend, and creates a single executable file.

**On Linux/macOS:**

```bash
# From the project root directory
go build -o mediahub ./cmd/mediahub
```

**On Windows (PowerShell):**

```powershell
# From the project root directory
go build -o mediahub.exe ./cmd/mediahub
```

This command creates a single executable file named `mediahub` (or `mediahub.exe`) in your project root. This file contains the entire application, including the web UI.

-----

## 📚 Code Overview

The application is a monorepo containing two main parts:

1.  **Go Backend API (`/cmd`, `/internal`):**

      * Provides RESTful API endpoints under `/api/` for managing file "databases" and the entries within them.
      * Handles file uploads, storage on the filesystem, and metadata management in a local SQLite database.
      * Serves the static files for the Angular frontend, which are embedded directly into the binary.

2.  **Angular Frontend (`/frontend`):**

      * A single-page application built with the Angular framework.
      * Provides a user interface for logging in, viewing databases, uploading files, and editing entry details.
      * Uses Angular's built-in router for navigation and HttpClient for API communication.
      * Dynamically adapts the UI based on the authenticated user's permissions.

-----

## 💼 Support and commercial features

You can use the free version for commercial use cases without any restrictions.
If you are in need of software support, specific features or similar, please [contact me](denglerchr@gmail.com). 

-----

## 🔣 Miscellaneous

While this application is not "vibe-coded", the development is heavily supported by AI code generation for coding efficiency. If you have a no-AI software policy, you should not use this program.
