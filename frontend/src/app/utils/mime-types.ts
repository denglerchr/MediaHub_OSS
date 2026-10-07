// frontend/src/app/utils/mime-types.ts
import { ContentType } from '../models/enums';

export interface MimeConfig {
  mime: string;
  streamable: boolean;
}

/**
 * A centralized map of allowed MIME types for each ContentType,
 * including browser compatibility flags like streamability.
 */
export const ALLOWED_MIME_TYPES: Record<ContentType, MimeConfig[]> = {
  [ContentType.Image]: [
    { mime: 'image/jpeg', streamable: false },
    { mime: 'image/png', streamable: false },
    { mime: 'image/gif', streamable: false },
    { mime: 'image/webp', streamable: false },
    { mime: 'image/avif', streamable: false },
  ],
  [ContentType.Audio]: [
    { mime: 'audio/mpeg', streamable: true },
    { mime: 'audio/wav', streamable: true },
    { mime: 'audio/flac', streamable: true },
    { mime: 'audio/opus', streamable: true },
    { mime: 'audio/ogg', streamable: true },
    { mime: 'application/ogg', streamable: true },
    { mime: 'audio/x-flac', streamable: true },
    { mime: 'audio/m4a', streamable: true },
    { mime: 'audio/mp4', streamable: true },
  ],
  [ContentType.Video]: [
    { mime: 'video/mp4', streamable: true },
    { mime: 'video/webm', streamable: true },
    { mime: 'video/ogg', streamable: true },
    // Formats below are not natively supported for streaming by most HTML5 players
    { mime: 'video/x-matroska', streamable: false },
    { mime: 'video/quicktime', streamable: false },
    { mime: 'video/x-msvideo', streamable: false },
    { mime: 'video/x-flv', streamable: false },
  ],
  [ContentType.File]: [],
};

/**
 * Validates if a given MIME type is allowed for the specific ContentType.
 */
export function isMimeTypeAllowed(contentType: ContentType, mimeType: string): boolean {
  const allowed = ALLOWED_MIME_TYPES[contentType];
  if (!allowed || allowed.length === 0) {
    return true;
  }
  return allowed.some((config) => config.mime === mimeType);
}

/**
 * Checks if the given MIME type can be natively streamed by the browser.
 */
export function isMimeTypeStreamable(mimeType: string): boolean {
  if (!mimeType) return false;

  for (const key in ALLOWED_MIME_TYPES) {
    const configs = ALLOWED_MIME_TYPES[key as ContentType];
    const found = configs.find((c) => c.mime === mimeType);
    if (found) {
      return found.streamable;
    }
  }

  return false; // Default to false for unknown or generic files
}

/**
 * Generates an accept string for HTML file inputs based on content type.
 */
export function getFileAcceptString(contentType: string): string | null {
  const allowedConfig = ALLOWED_MIME_TYPES[contentType as ContentType];
  if (allowedConfig && allowedConfig.length > 0) {
    let accept = allowedConfig.map((config) => config.mime).join(',');
    if (contentType === 'audio') {
      accept += ',.m4a,.mp3,.flac,.wav,.ogg,.opus';
    }
    return accept;
  }
  return null;
}
