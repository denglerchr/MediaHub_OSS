# Keycloak OIDC SSO Example

This directory contains a complete local development and testing environment with Keycloak as the Identity Provider (IdP) and MediaHub configured for OIDC Single Sign-On.

## Features

- **Keycloak 26.1** running on port `8081`.
- **Pre-configured realm (`mediahub`)** automatically imported on startup with:
  - Client ID: `mediahub`
  - Client Secret: `mediahub-secret-1234`
  - Valid Redirect URIs: `http://localhost:4200/*`, `http://localhost:8080/*` (Identity Provider must redirect to `/auth/callback`)
  - Pre-seeded test accounts:
    - Username: `john.doe` | Password: `password123` | Email: `john.doe@example.com`
    - Username: `alice.smith` | Password: `password123` | Email: `alice.smith@example.com`
- **Keycloak Admin Console**: `http://localhost:8081` (Credentials: `admin` / `admin`).

## Running with Docker Compose

To start both Keycloak and MediaHub:

```bash
docker compose up -d
```

Keycloak will start up and import the `mediahub` realm. Once healthy, MediaHub will start up at `http://localhost:8080`.

## Running Local Backend / Frontend with Keycloak Container

If you are developing locally and want to run the Go backend or Angular frontend directly on your host machine:

1. Start only Keycloak:
   ```bash
   docker compose up keycloak -d
   ```
2. Run the Go backend on your host (the `--auth.oidc.redirect_url` can be omitted as the frontend defaults to `<origin>/auth/callback`):
   ```bash
   go run ./cmd/mediahub serve \
     --auth.oidc.enabled=true \
     --auth.oidc.issuer_url=http://localhost:8081/realms/mediahub \
     --auth.oidc.client_id=mediahub \
     --auth.oidc.client_secret=mediahub-secret-1234 \
     --auth.oidc.redirect_url=http://localhost:4200/auth/callback
   ```
3. Run the Angular frontend dev server:
   ```bash
   cd frontend
   npm start
   ```
4. Open `http://localhost:4200/` and log in via Single Sign-On.
