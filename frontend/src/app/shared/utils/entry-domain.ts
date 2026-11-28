import { Entry } from '../../core/models/entry.model';
import { ContentType, EntryStatus } from '../../core/models/enums';

export const CONTENT_TYPE_MEDIA_FIELDS: Readonly<Record<ContentType, readonly string[]>> = {
  [ContentType.Image]: ['width', 'height'],
  [ContentType.Audio]: ['duration', 'channels'],
  [ContentType.Video]: ['width', 'height', 'duration'],
  [ContentType.File]: [],
};

const AUTO_PREVIEW_CONTENT_TYPES: ReadonlySet<string> = new Set([
  ContentType.Image,
  ContentType.Audio,
  ContentType.Video,
]);

export interface AutoConversionOption {
  value: string;
  label: string;
}

const AUTO_CONVERSION_OPTIONS: Readonly<Record<string, readonly AutoConversionOption[]>> = {
  [ContentType.Image]: [
    { value: '', label: 'None' },
    { value: 'image/jpeg', label: 'JPEG' },
    { value: 'image/webp', label: 'WebP' },
    { value: 'image/avif', label: 'AVIF' },
  ],
  [ContentType.Audio]: [
    { value: '', label: 'None' },
    { value: 'audio/flac', label: 'FLAC' },
    { value: 'audio/opus', label: 'Opus' },
  ],
  [ContentType.Video]: [
    { value: '', label: 'None' },
    { value: 'video/mp4', label: 'MP4 (H.264 codec)' },
    { value: 'video/webm', label: 'WebM (AV1 codec)' },
  ],
};

/**
 * Returns the standard media metadata field names associated with a content type.
 */
export function getMediaFieldsForContentType(
  contentType: ContentType | string | undefined,
): readonly string[] {
  if (
    !contentType ||
    !Object.prototype.hasOwnProperty.call(CONTENT_TYPE_MEDIA_FIELDS, contentType)
  ) {
    return [];
  }
  return CONTENT_TYPE_MEDIA_FIELDS[contentType as ContentType];
}

/**
 * True when the database content type supports automatic preview generation / configuration.
 */
export function supportsAutoPreview(contentType: ContentType | string | undefined): boolean {
  return typeof contentType === 'string' && AUTO_PREVIEW_CONTENT_TYPES.has(contentType);
}

/**
 * Returns the available auto-conversion target options for a database content type.
 */
export function getAutoConversionOptions(
  contentType: ContentType | string | null | undefined,
): readonly AutoConversionOption[] {
  if (
    typeof contentType !== 'string' ||
    !Object.prototype.hasOwnProperty.call(AUTO_CONVERSION_OPTIONS, contentType)
  ) {
    return [];
  }
  return AUTO_CONVERSION_OPTIONS[contentType];
}

/**
 * Returns the explanatory hint shown below the auto-conversion selector for a content type.
 */
export function getMediaConfigHint(contentType: ContentType | string | null | undefined): string {
  if (contentType === ContentType.Video) {
    return 'Converts uploads to the selected format. Transcoding video files can take significant time.';
  }
  return 'Converts uploads to the selected format.';
}

export function isEntryProcessing(entry: Pick<Entry, 'status'> | null | undefined): boolean {
  return entry?.status === EntryStatus.Processing;
}

export function isEntryQueued(entry: Pick<Entry, 'status'> | null | undefined): boolean {
  return entry?.status === EntryStatus.Queued;
}

export function isEntryReady(entry: Pick<Entry, 'status'> | null | undefined): boolean {
  return entry?.status === EntryStatus.Ready;
}

export function isEntryError(entry: Pick<Entry, 'status'> | null | undefined): boolean {
  return entry?.status === EntryStatus.Error;
}

export function isEntryDeleting(entry: Pick<Entry, 'status'> | null | undefined): boolean {
  return entry?.status === EntryStatus.Deleting;
}

export function isEntryBusy(entry: Pick<Entry, 'status'> | null | undefined): boolean {
  return isEntryProcessing(entry) || isEntryQueued(entry);
}

/**
 * Entries can be edited unless they are actively processing or deleting (queued entries are editable).
 */
export function canEditEntry(entry: Pick<Entry, 'status'> | null | undefined): boolean {
  if (!entry) {
    return false;
  }
  return !isEntryProcessing(entry) && !isEntryDeleting(entry);
}

/**
 * Entries can be deleted unless they are processing, queued, or already deleting.
 */
export function canDeleteEntry(entry: Pick<Entry, 'status'> | null | undefined): boolean {
  if (!entry) {
    return false;
  }
  return !isEntryProcessing(entry) && !isEntryQueued(entry) && !isEntryDeleting(entry);
}
