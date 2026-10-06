---
sidebar_position: 3
title: Uploading Media Entries
---

# Uploading Media Entries

Entries can be uploaded via the Angular Web UI or programmatically via HTTP REST endpoints.

---

## 💻 Uploading via Web UI

1. Open a database from the main dashboard.
2. Click **Upload Entry** or **Bulk Upload**.
3. Select media files from your machine.
4. Fill in custom metadata values defined for the target database.
5. Click **Submit Upload**.

---

## 📍 Automatic Metadata Extraction *(New in v3.2)*

When files are selected in the upload dialog, MediaHub inspects them locally in the browser and pre-fills the form:

* **Capture timestamp**: extracted from JPEG EXIF data and from MP4 movies (`creation_time`).
* **GPS location**: extracted from JPEG EXIF GPS tags and written into every `COORDINATE` custom field of the database.
* **Bulk uploads**: extraction runs **per file** — each file in a multi-file upload gets its own timestamp and coordinates. Files without embedded metadata fall back to the values entered in the form (timestamp defaults to the current time).
* The extracted values are only pre-filled: they remain visible and editable in the form before submitting, so you can override or clear them.

> **Note:** Extraction happens entirely client-side; no metadata is sent to external services.

---

## ⚡ Synchronous vs. Asynchronous Upload Threshold

MediaHub optimizes memory and disk utilization during uploads using the `max_sync_upload_size` configuration setting (default `4MB`):

* **Synchronous Processing (File Size ≤ `max_sync_upload_size`)**:
  * Processed entirely in RAM for maximum throughput.
  * Transcoding and preview creation occur synchronously before returning the API HTTP response.
* **Asynchronous Processing (File Size > `max_sync_upload_size`)**:
  * Streamed to temporary disk storage.
  * Transcoding runs in a background worker pool (`n_max_queued` configuration).
  * Returns an HTTP `202 Accepted` response immediately while processing completes asynchronously.

---

## 📤 Programmatic API Upload Example

Upload a file with custom metadata using `curl`:

```bash
curl -X POST "http://localhost:8080/api/database/01HGFB9Z5W7ABCDEFGHJKMNPQR/entry" \
  -H "Authorization: Bearer srv_your_api_key_here" \
  -F "file=@/path/to/camera_capture.jpg" \
  -F 'metadata={"timestamp": 1780713653123, "filename": "camera_capture.jpg", "custom_fields": {"location": {"latitude": 49.6116, "longitude": 6.1319}, "defect_detected": false}}'
```

> **Note:** Custom field values must be nested under `custom_fields` — any other keys in the `metadata` JSON are ignored. `COORDINATE` values are objects (`{"latitude": …, "longitude": …}`).
