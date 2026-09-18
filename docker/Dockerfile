# Build the application (backend and frontend) as a docker image
# -> run in the project root folder `docker build -t denglerchr/mediahub_oss:latest -f ./docker/Dockerfile .`
# or multiarch build
# -> run `docker buildx build --platform linux/amd64,linux/arm64 -t denglerchr/mediahub_oss:latest -f ./docker/Dockerfile . --push`
# Start a container with, e.g., 
# docker run -d \
#   --name mediahub \
#   -p 8080:8080 \
#   -v $(pwd)/mediahub_config:/config \
#   -v $(pwd)/mediahub_storage:/storage \
#   -e MEDIAHUB_PASSWORD="admin-password-123" \
#   denglerchr/mediahub_oss:latest

# ==========================================
# STAGE 1: Frontend Builder
# ==========================================
FROM node:25-bookworm AS frontend-builder
WORKDIR /app/frontend

# Cache Node modules first
COPY frontend/package*.json ./
RUN npm ci --no-audit --no-fund

# Copy frontend source and build
COPY frontend/ ./
RUN npm run build -- --configuration production


# ==========================================
# STAGE 2: Backend Builder
# ==========================================
FROM golang:1.26-bookworm AS backend-builder
WORKDIR /app

# Cache Go modules first
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the backend code
COPY . .

# Copy frontend files
COPY --from=frontend-builder /app/cmd/mediahub/frontend_embed ./cmd/mediahub/frontend_embed

# Build Backend
RUN go build -trimpath -ldflags="-s -w" -o /app/mediahub ./cmd/mediahub


# ==========================================
# STAGE 3: The Final Production Image
# ==========================================
FROM debian:12-slim
ARG DEBIAN_FRONTEND=noninteractive

# 1. Install runtime basics (ca-certificates needed for outbound HTTPS requests)
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# 2. Grab FFmpeg seamlessly
# Docker automatically handles pulling the ARM64 or AMD64 version based on your build target!
COPY --from=mwader/static-ffmpeg:8.1 /ffmpeg /usr/local/bin/
COPY --from=mwader/static-ffmpeg:8.1 /ffprobe /usr/local/bin/

# 3. Setup folders
RUN mkdir /config /storage

# 4. Copy artifacts
WORKDIR /app
COPY --from=backend-builder /app/mediahub .
COPY --from=backend-builder /app/config.toml /config/config.toml

# 5. Config
ENV MEDIAHUB_DATABASE_PATH=/config/mediahub.db \
    MEDIAHUB_STORAGE_TYPE=local \
    MEDIAHUB_DATABASE_DRIVER=sqlite \
    MEDIAHUB_DATABASE_SOURCE=/storage/mediahub.db \
    MEDIAHUB_STORAGE_LOCAL_ROOT=/storage \
    MEDIAHUB_CONFIG_PATH=/config/config.toml \
    MEDIAHUB_INIT_CONFIG=/config/initconfig.toml

EXPOSE 8080
ENTRYPOINT ["./mediahub"]
CMD ["serve"]