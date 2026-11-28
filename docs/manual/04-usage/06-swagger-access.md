---
sidebar_position: 6
title: Interactive Swagger API Documentation
---

# Interactive Swagger API Documentation

MediaHub automatically serves an interactive **OpenAPI 3.1 / Swagger UI v5** explorer directly from the running web server.

---

## 🚀 Accessing Swagger UI

You can access the interactive Swagger documentation in two ways:

1. **Via Web UI Button**:
   * Log into the MediaHub Web Interface.
   * Click the **API Documentation** (or Swagger) button located in the top navigation header / account menu.
2. **Direct Browser URL**:
   * Navigate directly to:
     ```text
     http://localhost:8080/swagger/
     ```
   * The raw OpenAPI 3.1 JSON specification is available at `http://localhost:8080/swagger/doc.json`.

---

## ⚡ Using Swagger UI

1. **Interactive Endpoint Explorer**: Expand any endpoint category to inspect request payloads, headers, query parameters, and response schemas.
2. **Authentication**:
   * Click the **Authorize** button at the top right of the Swagger interface.
   * Choose **BasicAuth** (username/password) or **BearerAuth**. For **BearerAuth**, paste only the raw JWT access token or API key (`srv_...`) — Swagger UI v5 automatically sends the `Bearer ` prefix.
3. **Regenerating OpenAPI Specifications** (For Developers):
   If backend handler Go code is modified, regenerate the OpenAPI 3.1 specification using `swag` v2 (`--v3.1`) prior to compiling:
   ```bash
   # Install swag v2 (once)
   go install github.com/swaggo/swag/v2/cmd/swag@v2.0.0-rc6

   # Regenerate OpenAPI 3.1 docs
   swag init -g ./cmd/mediahub/main.go --v3.1
   ```
