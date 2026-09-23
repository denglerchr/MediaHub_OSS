---
sidebar_position: 1
title: Overview & Features
slug: /
---

# Overview & Features

**MediaHub** is an open-source media asset management server and REST API for storing and retrieving images, audio, video, and generic files with custom metadata. 

Built with a **Go** backend and an embedded **Angular** web interface, MediaHub provides single-binary deployments with low memory usage, suitable for both edge device collection and enterprise server infrastructure.

---

## 🎯 Primary Use Cases

MediaHub is specifically designed for **data acquisition pipelines** and **edge media storage**:

* **Camera & Machine Vision Pipelines**: Storing image or video streams in production environments (e.g., quality control, defect detection, wildlife observation).
* **Audio Condition Monitoring**: Ingesting audio samples captured via microphones for industrial machine monitoring or environmental acoustics.
* **Edge Storage with Auto-Purging**: Operating on disk-constrained edge hardware with strict retention rules.
* **ML & AI Ingestion Pipelines**: Attaching custom metadata (e.g., model predictions, bounding boxes, sensor metrics) to stored media entries and querying via indexed fields.

Of course it can also be used to store media in other contexts, but in that case, other solutions might fit better.

---

## ✨ Key Features

* **Database & Storage Flexibility**:
  * **Database Drivers**: Built-in support for **SQLite** (single file / edge) and **PostgreSQL** (production scaling).
  * **Storage Drivers**: Local filesystem storage or **S3-compatible object storage** (AWS S3, MinIO, Wasabi, Ceph).
* **Dynamic Metadata & Custom Fields**:
  * Define custom typed metadata schemas (e.g., `confidence_score` [REAL], `location` [TEXT], `is_defective` [BOOLEAN]) per database.
  * Fields are indexed directly for fast range, comparison, and wildcard text searches (`>`, `<`, `>=`, `<=`, `!=`, `LIKE`).
* **Automated Housekeeping**:
  * Background worker periodically enforces retention rules.
  * Configure cleanup based on **maximum entry age** (e.g., 30 days, or `0` to disable) and **disk space limits** (e.g., 100GB, or `0` to disable).
* **Automated Media Transcoding**:
  * Automatic FFmpeg-powered media conversion on ingestion (e.g., raw camera frames to `JPEG`/`WebP`, audio to `FLAC`, video to `WebM`).
  * Automated preview generation for UI thumbnail viewing.
* **Hybrid Authentication & RBAC**:
  * Support for **OpenID Connect (OIDC / SSO)** (e.g., Keycloak, Authentik, Okta) with automatic user provisioning.
  * Local **Basic Auth**, **JWT Access/Refresh tokens**, and long-lived **API Keys** with scope restrictions.
  * Fine-grained Role-Based Access Control (RBAC) per database (`can_view`, `can_create`, `can_edit`, `can_delete`, `can_admin`).
  * Dedicated administrator break-glass bypass (`/login?local=1`) when OIDC-only mode is enforced.
* **Single Binary Deployment**:
  * The Angular web UI is compiled and embedded into the Go executable for zero-dependency execution.
* **Bulk Export & Import**:
  * Export and import databases and media entries via compressed ZIP archives.

---

## 🔍 Decision Guide: MediaHub vs. Immich / PhotoPrism

If you are deciding which self-hosted media platform to choose, consider the core focus of each project:

| Feature / Goal | **MediaHub** | **Immich / PhotoPrism / LibrePhotos** |
| :--- | :--- | :--- |
| **Primary Audience** | Industrial pipelines, edge devices, ML/AI engineers, developers | Personal photo backup, families, privacy-conscious consumers |
| **Use-Case Target** | 24/7 automated media acquisition, machine telemetry, sensor storage | Cloud photo alternative (Google Photos / Apple Photos replacement) |
| **Data Retention / Cleanup**| **Built-in automated housekeeping** by age & disk usage | Intended for permanent personal photo archiving |
| **Metadata Management** | **Custom indexed schema fields** per database (e.g. model confidence) | Fixed EXIF, facial recognition, geocoding, album tagging |
| **API & Service Accounts** | First-class API keys with granular scopes (`scope_create`, `scope_view`) | User-centric web authentication and mobile app sync |
| **Transcoding Goal** | Space reduction & edge optimization (e.g., WebP/FLAC conversion) | High-fidelity photo preservation and HLS video streaming |

> **Summary**: If you need to back up your personal smartphone photo album, tools like **Immich** or **PhotoPrism** are ideal. If you are building an automated camera system, IoT acoustic monitor, or ML pipeline that ingests media 24/7 with strict storage quotas and custom metadata, **MediaHub** is designed for your workflow.
