---
sidebar_position: 8
title: Single Entry Management
---

# Single Entry Management Endpoints

Endpoints for uploading, inspecting metadata, updating, downloading raw files or previews, and deleting individual media entries.

---

## `POST /api/database/{database_id}/entry`

Uploads a single media file into a database using `multipart/form-data`.

* **Role Required**: `can_create` on database
* **Path Parameters**:
  * `database_id` (string, required): ULID of the target database.
* **Headers**:
  * `Content-Type`: `multipart/form-data; boundary=...`
* **Form Data Parts**:
  1. `metadata` (text field): Serialized JSON string containing entry timestamp, original filename, and custom field values.
     ```json
     {
       "timestamp": 1780713653123,
       "filename": "my_song.wav",
       "custom_fields": {
         "artist": "Demo",
         "album": "Demo Album"
       }
     }
     ```
     * **`COORDINATE` custom fields** *(New in v3.2)* are sent as an object: `{"location": {"latitude": 48.137154, "longitude": 11.576124}}` (aliases `lat` / `lon` / `lng` are accepted; latitude must be within `-90…90`, longitude within `-180…180`).
  2. `file` (binary file part): Raw binary content of the file.
  3. `preview` (optional binary file part, `Content-Type: image/webp`): Custom WebP preview image (static or animated WebP allowed, max **128 KiB** / `131,072` bytes, max **200 × 200** pixels). When provided, automatic preview generation is skipped even if `create_preview` is enabled, and custom previews are accepted across all database `content_type`s (including `file`). Invalid or oversized previews fail with `400`, `413`, or `415`.

### Response Case 1: Synchronous Processing (`201 Created`)
Returned for small, synchronous uploads.
```json
{
  "database_id": "01HGFB9Z5W7ABCDEFGHJKMNPQR",
  "id": 10232,
  "timestamp": 1780713653123,
  "created_at": 1780713655000,
  "updated_at": 1780713655000,
  "filesize": 8945000,
  "preview_filesize": 800,
  "mime_type": "audio/flac",
  "filename": "my_song.wav",
  "status": "ready",
  "media_fields": {
    "duration": 150.2,
    "channels": 2
  },
  "custom_fields": {
    "artist": "Demo",
    "album": "Demo Album"
  }
}
```

### Response Case 2: Asynchronous Processing (`202 Accepted`)
Returned for large files or queued jobs. The client should poll `GET /api/database/{database_id}/entry/{id}` until `status` becomes `"ready"`.

---

## `GET /api/database/{database_id}/entry/{id}`

Retrieves all metadata for a single entry.

* **Role Required**: `can_view` on database
* **Path Parameters**:
  * `database_id` (string, required): Database ULID.
  * `id` (integer, required): Unique entry ID.
* **Response (`200 OK`)**: Returns entry metadata object.

---

## `PATCH /api/database/{database_id}/entry/{id}`

Updates mutable metadata (`timestamp`, `filename`, `custom_fields`) of an existing entry.

* **Role Required**: `can_edit` on database
* **Path Parameters**:
  * `database_id` (string, required): Database ULID.
  * `id` (integer, required): Unique entry ID.
* **Request Body**:
  ```json
  {
    "timestamp": 1790000000123,
    "filename": "a_new_name.flac",
    "custom_fields": {
      "artist": "A new artist"
    }
  }
  ```
* **Response (`200 OK`)**: Returns updated entry metadata object.

---

## `GET /api/database/{database_id}/entry/{id}/file`

Retrieves the raw file binary or an on-the-fly transformed derivative. Supports **HTTP Range Requests** (streaming/seeking for untransformed files), **Content Negotiation** (binary vs JSON Base64), and **On-The-Fly Media Transformations** (format conversion and image resizing).

* **Role Required**: `can_view` on database
* **Path Parameters**:
  * `database_id` (string, required): Database ULID.
  * `id` (integer, required): Unique entry ID.
* **Query Parameters (Optional — On-The-Fly Transformations)**:
  * `format` (string): Desired output format or MIME type. Accepts either a short format name (e.g., `webp`, `jpeg`, `png`, `flac`, `mp3`, `opus`) or a full MIME type (e.g., `image/webp`, `audio/ogg`). Common aliases like `jpg` and `mp3` are automatically normalized.
    * **Supported Image Formats**: `jpeg` (`image/jpeg`), `png` (`image/png`), `webp` (`image/webp`), `gif` (`image/gif`), `avif` (`image/avif`).
    * **Supported Audio Formats**: `mp3` (`audio/mpeg`), `wav` (`audio/wav`), `ogg` (`audio/ogg`), `flac` (`audio/flac`), `opus` (`audio/opus`).
    * Any other target format returns `400 Bad Request`.
  * `width` (integer, `> 0`): Target width in pixels (`image` databases only).
  * `height` (integer, `> 0`): Target height in pixels (`image` databases only).
    * If only one of `width` or `height` is provided, the other dimension is calculated automatically to preserve the original aspect ratio (`fit` is ignored).
  * `fit` (string): Scaling behavior when both `width` and `height` are specified (`image` databases only). Defaults to `cut`:
    * `cut` *(default)*: Scales the image to fill the target `width` × `height` box while preserving aspect ratio, then center-crops any overflowing edges.
    * `stretch`: Scales the image to the exact `width` × `height` dimensions without preserving aspect ratio.
    * `pad-white`: Scales the image to fit inside the `width` × `height` box while preserving aspect ratio, padding any remaining space with white bars.
    * `pad-black`: Scales the image to fit inside the `width` × `height` box while preserving aspect ratio, padding any remaining space with black bars.
* **Headers (Optional)**:
  * `Accept`: `*/*` (default binary download) or `application/json` (Base64 data wrapper).
  * `Range`: `bytes=0-1023` (Triggers `206 Partial Content` streaming response when `Accept` is binary and no transformation is requested).

### Transformation Rules by Database Content Type
* **`image`**: Supports `format`, `width`, `height`, and `fit`.
* **`audio`**: Supports `format`. Providing `width`, `height`, or `fit` returns `400 Bad Request`.
* **`video`**: On-the-fly transformations are not supported; providing any transformation parameter returns `501 Not Implemented`.
* **`file`**: Transformations are not applicable; providing any transformation parameter returns `400 Bad Request`.

> **Note**: On-the-fly transformations do not modify the stored entry in the database or on disk. If the requested `format` matches the stored file's MIME type and no resizing is requested, conversion is bypassed and the stored file is served directly.

### Binary Response (`200 OK` / `206 Partial Content`)
Returns binary stream with standard headers (`Content-Type`, `Content-Length`, `Content-Disposition` with updated file extension when transformed, plus `Accept-Ranges` / `Content-Range` for untransformed files).

### JSON Base64 Response (`200 OK` when `Accept: application/json`)
When transformed, `filename`, `mime_type`, and `size` reflect the transformed output:
```json
{
  "filename": "my_song.wav",
  "mime_type": "audio/wav",
  "size": 8945000,
  "data": "data:audio/wav;base64,UklGRi..."
}
```

### Additional Error Responses
* `400 Bad Request`: Invalid dimensions, unknown `fit` mode, unsupported target format, or disallowed parameters for the database content type.
* `409 Conflict`: Entry is still being processed.
* `501 Not Implemented`: Transformation requested on a `video` database.
* `503 Service Unavailable`: Server transformation concurrency limit reached.

---

## `GET /api/database/{database_id}/entry/{id}/preview`

Retrieves the generated or manually uploaded WebP preview thumbnail for an entry (`image/webp`). Supported across all database `content_type`s (including `file`). Supports **Content Negotiation**.

* **Role Required**: `can_view` on database
* **Path Parameters**:
  * `database_id` (string, required): Database ULID.
  * `id` (integer, required): Unique entry ID.
* **Headers (Optional)**:
  * `Accept`: `*/*` (default binary image) or `application/json` (Base64 wrapper).

### Response (`200 OK`)
Returns binary WebP payload (with `Cache-Control: no-cache`) or JSON Base64 string payload.

---

## `POST /api/database/{database_id}/entry/{id}/preview`

Adds a custom WebP preview to an existing entry **only if it does not already have one** (`preview_filesize == 0`). Supported across all database `content_type`s (including `file`), regardless of `create_preview`. Designed for programmatic clients.

* **Role Required**: `can_create` on database
* **Path Parameters**:
  * `database_id` (string, required): Database ULID.
  * `id` (integer, required): Unique entry ID.
* **Headers**:
  * `Content-Type` (required): `image/webp` *(JSON and `multipart/form-data` are rejected with `415 Unsupported Media Type`)*.
* **Request Body & Constraints**: Raw binary WebP image (static or animated WebP allowed), max **128 KiB** (`131,072` bytes), dimensions `1..200 × 1..200` pixels.
* **Response (`201 Created`)**:
  ```json
  {
    "database_id": "01HGFB9Z5W7ABCDEFGHJKMNPQR",
    "id": 10232,
    "preview_filesize": 9420,
    "updated_at": 1790000010000
  }
  ```
* **Error Responses**: `400 Bad Request` (empty/malformed WebP or invalid dimensions), `404 Not Found` (missing DB/entry), `409 Conflict` (preview already exists or entry is `processing`, `queued`, or `deleting`), `413 Payload Too Large` (`> 128 KiB`), `415 Unsupported Media Type`.

---

## `PUT /api/database/{database_id}/entry/{id}/preview`

Creates or replaces the custom WebP preview for an existing entry (**upsert**). Uses the same `Content-Type: image/webp` raw binary request body and validation limits (`128 KiB`, `200 × 200` px, static or animated WebP allowed).

* **Role Required**: `can_edit` on database
* **Path Parameters**:
  * `database_id` (string, required): Database ULID.
  * `id` (integer, required): Unique entry ID.
* **Response (`200 OK`)**:
  ```json
  {
    "database_id": "01HGFB9Z5W7ABCDEFGHJKMNPQR",
    "id": 10232,
    "preview_filesize": 10240,
    "updated_at": 1790000020000
  }
  ```
* **Error Responses**: `400`, `404`, `409` (entry is `processing`, `queued`, or `deleting`), `413`, `415`.

---

## `DELETE /api/database/{database_id}/entry/{id}/preview`

Deletes the entry's stored preview image, resets `preview_filesize` to `0`, and updates database disk space statistics.

* **Role Required**: `can_delete` on database
* **Path Parameters**:
  * `database_id` (string, required): Database ULID.
  * `id` (integer, required): Unique entry ID.
* **Response (`200 OK`)**:
  ```json
  {
    "message": "Preview for entry '10232' in database '01HGFB9Z5W7ABCDEFGHJKMNPQR' was successfully deleted."
  }
  ```
* **Error Responses**: `404 Not Found` (missing DB, entry, or preview), `409 Conflict` (entry is `processing`, `queued`, or `deleting`).

---

## `DELETE /api/database/{database_id}/entry/{id}`

Deletes a media entry, removing its stored file, preview, and metadata from database.

* **Role Required**: `can_delete` on database
* **Path Parameters**:
  * `database_id` (string, required): Database ULID.
  * `id` (integer, required): Unique entry ID.
* **Response (`200 OK`)**:
  ```json
  {
    "message": "Entry '10232' from database '01HGFB9Z5W7ABCDEFGHJKMNPQR' was successfully deleted."
  }
  ```
