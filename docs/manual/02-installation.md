---
sidebar_position: 2
title: Installation
---

# Installation Guide

MediaHub can be deployed as a **standalone native binary** or as a **Docker container**. 

---

## 📦 Native Binary Installation

Prebuilt binaries are available for Linux and Windows across `x86_64` and `aarch64` (ARM64) architectures.

### 1. Download the Binary & Configuration
Download the appropriate executable from the official distribution links:

| Operating System | Architecture | Download Link |
| :--- | :--- | :--- |
| **Linux** | AArch64 (ARM 64-bit) | [mediahub_linux_aarch64](https://downloads.swcd.lu/MediaHub/latest/mediahub_linux_aarch64) |
| **Linux** | x86_64 (AMD/Intel 64-bit) | [mediahub_linux_x86_64](https://downloads.swcd.lu/MediaHub/latest/mediahub_linux_x86_64) |
| **Windows** | AArch64 (ARM 64-bit) | [mediahub_windows_aarch64.exe](https://downloads.swcd.lu/MediaHub/latest/mediahub_windows_aarch64.exe) |
| **Windows** | x86_64 (AMD/Intel 64-bit) | [mediahub_windows_x86_64.exe](https://downloads.swcd.lu/MediaHub/latest/mediahub_windows_x86_64.exe) |

Download a template `config.toml` file [here](https://downloads.swcd.lu/MediaHub/latest/config.toml).

```bash
# Download template config.toml
curl -O https://downloads.swcd.lu/MediaHub/v3.0.0/config.toml
```

### 2. Prerequisites
* **FFmpeg & FFprobe**: Required on the host system path (or specified in `config.toml`) for automatic media conversion, thumbnail preview generation, and metadata extraction.

### 3. Run the Server
Make the binary executable (Linux/macOS) and launch the web server:

```bash
# Give execution permissions (Linux)
chmod +x mediahub_linux_x86_64

# Start the server with default config.toml in current directory
./mediahub_linux_x86_64 serve

# Or specify a custom config path and initial admin password
./mediahub_linux_x86_64 serve --config_path /etc/mediahub/config.toml --password "MySecretPassword123"
```

The web server will start by default at `http://localhost:8080`.

---

## 🐳 Docker Installation

The official Docker image [`denglerchr/mediahub_oss`](https://hub.docker.com/r/denglerchr/mediahub_oss) includes all required dependencies (including FFmpeg).

### 1. Volume Mounts & Persistence

When running via Docker, configure volume mounts for persistence:
* **Storage Volume (`/storage`)**: Mount a host directory to `/storage` to persist uploaded media files when using local storage backend.
* **Configuration / Working Directory**: If using a `config.toml` file, place your configuration file in a host directory (e.g. `mediahub_config`) and mount it or pass settings via environment variables.

### 2. Basic Docker Run Command

Place your config file in a folder `mediahub_config` (or configure via environment variables) and run:

```bash
docker run -d \
  --name mediahub \
  -p 8080:8080 \
  -v $(pwd)/mediahub_storage:/storage \
  -e MEDIAHUB_PASSWORD="your-secure-password" \
  denglerchr/mediahub_oss:latest
```

### 3. Docker Compose Example

Recommended deployment using `docker-compose.yml`:

```yaml
version: '3.8'

services:
  mediahub:
    image: denglerchr/mediahub_oss:latest
    container_name: mediahub
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      - MEDIAHUB_SERVER_HOST=0.0.0.0
      - MEDIAHUB_SERVER_PORT=8080
      - MEDIAHUB_DATABASE_DRIVER=sqlite
      - MEDIAHUB_DATABASE_SOURCE=mediahub.db
      - MEDIAHUB_STORAGE_TYPE=local
      - MEDIAHUB_STORAGE_LOCAL_ROOT=/storage
      - MEDIAHUB_PASSWORD=my-secure-password
    volumes:
      - ./mediahub_storage:/storage
```

### 4. OIDC / Keycloak Example Stack *(New in v3.2)*

A ready-to-run Compose stack that starts MediaHub together with a Keycloak server (port `8081`, Keycloak 26.1) pre-loaded with a `mediahub` realm (client ID `mediahub`, valid redirect URIs `http://localhost:4200/*` and `http://localhost:8080/*`) and two test accounts is included in the repository:

```bash
docker/examples/oidc_keycloak/compose.yaml
```

See [`docker/examples/oidc_keycloak/README.md`](https://github.com/denglerchr/MediaHub_OSS/blob/main/docker/examples/oidc_keycloak/README.md) for setup instructions (including running a local backend/frontend against the Keycloak container), and [Configuration](./03-configuration.md) for the matching `[auth.oidc]` settings.

---

## 🔒 Recommended Docker Configuration via Environment Variables

In containerized deployments, **configuring via environment variables** is strongly recommended over editing static files. Every setting in `config.toml` can be overridden by an environment variable prefixed with `MEDIAHUB_`.

Example production environment variables:
```bash
MEDIAHUB_PASSWORD="SuperSecretAdminPassword"
MEDIAHUB_DATABASE_DRIVER="postgres"
MEDIAHUB_DATABASE_SOURCE="host=postgres user=mediahub password=secret dbname=mediahub port=5432 sslmode=disable"
MEDIAHUB_STORAGE_TYPE="s3"
MEDIAHUB_STORAGE_S3_ENDPOINT="s3.us-east-1.amazonaws.com"
MEDIAHUB_STORAGE_S3_BUCKET="my-production-media-bucket"
MEDIAHUB_STORAGE_S3_ACCESS_KEY="AKIA..."
MEDIAHUB_STORAGE_S3_SECRET_KEY="secret..."
```
