import { ContentType, EntryStatus } from '../../core/models/enums';
import {
  CONTENT_TYPE_MEDIA_FIELDS,
  canDeleteEntry,
  canEditEntry,
  getAutoConversionOptions,
  getMediaConfigHint,
  getMediaFieldsForContentType,
  isEntryBusy,
  isEntryDeleting,
  isEntryError,
  isEntryProcessing,
  isEntryQueued,
  isEntryReady,
  supportsAutoPreview,
} from './entry-domain';

describe('entry-domain utilities', () => {
  describe('CONTENT_TYPE_MEDIA_FIELDS & getMediaFieldsForContentType', () => {
    it('maps every ContentType to its media metadata fields', () => {
      expect(CONTENT_TYPE_MEDIA_FIELDS).toEqual({
        [ContentType.Image]: ['width', 'height'],
        [ContentType.Audio]: ['duration', 'channels'],
        [ContentType.Video]: ['width', 'height', 'duration'],
        [ContentType.File]: [],
      });

      expect(getMediaFieldsForContentType(ContentType.Image)).toEqual(['width', 'height']);
      expect(getMediaFieldsForContentType('audio')).toEqual(['duration', 'channels']);
      expect(getMediaFieldsForContentType('video')).toEqual(['width', 'height', 'duration']);
      expect(getMediaFieldsForContentType('file')).toEqual([]);
      expect(getMediaFieldsForContentType(undefined)).toEqual([]);
      expect(getMediaFieldsForContentType('toString')).toEqual([]);
    });
  });

  describe('supportsAutoPreview', () => {
    it('returns true for image, audio, and video, and false for file/unknown', () => {
      expect(supportsAutoPreview(ContentType.Image)).toBeTrue();
      expect(supportsAutoPreview('audio')).toBeTrue();
      expect(supportsAutoPreview('video')).toBeTrue();
      expect(supportsAutoPreview(ContentType.File)).toBeFalse();
      expect(supportsAutoPreview(undefined)).toBeFalse();
      expect(supportsAutoPreview('other')).toBeFalse();
    });
  });

  describe('getAutoConversionOptions & getMediaConfigHint', () => {
    it('returns expected conversion options per content type', () => {
      expect(getAutoConversionOptions(ContentType.Image)).toEqual([
        { value: '', label: 'None' },
        { value: 'image/jpeg', label: 'JPEG' },
        { value: 'image/webp', label: 'WebP' },
        { value: 'image/avif', label: 'AVIF' },
      ]);
      expect(getAutoConversionOptions('audio')).toEqual([
        { value: '', label: 'None' },
        { value: 'audio/flac', label: 'FLAC' },
        { value: 'audio/opus', label: 'Opus' },
      ]);
      expect(getAutoConversionOptions('video')).toEqual([
        { value: '', label: 'None' },
        { value: 'video/mp4', label: 'MP4 (H.264 codec)' },
        { value: 'video/webm', label: 'WebM (AV1 codec)' },
      ]);
      expect(getAutoConversionOptions(ContentType.File)).toEqual([]);
      expect(getAutoConversionOptions(null)).toEqual([]);
      expect(getAutoConversionOptions(undefined)).toEqual([]);
      expect(getAutoConversionOptions('toString')).toEqual([]);
    });

    it('returns video-specific hint for video and generic hint otherwise', () => {
      expect(getMediaConfigHint(ContentType.Video)).toBe(
        'Converts uploads to the selected format. Transcoding video files can take significant time.',
      );
      expect(getMediaConfigHint('video')).toBe(
        'Converts uploads to the selected format. Transcoding video files can take significant time.',
      );
      expect(getMediaConfigHint(ContentType.Image)).toBe(
        'Converts uploads to the selected format.',
      );
      expect(getMediaConfigHint('audio')).toBe('Converts uploads to the selected format.');
      expect(getMediaConfigHint(null)).toBe('Converts uploads to the selected format.');
      expect(getMediaConfigHint(undefined)).toBe('Converts uploads to the selected format.');
    });
  });

  describe('entry status predicates and action guards', () => {
    it('identifies each EntryStatus accurately', () => {
      expect(isEntryProcessing({ status: EntryStatus.Processing })).toBeTrue();
      expect(isEntryProcessing({ status: EntryStatus.Ready })).toBeFalse();

      expect(isEntryQueued({ status: EntryStatus.Queued })).toBeTrue();
      expect(isEntryReady({ status: EntryStatus.Ready })).toBeTrue();
      expect(isEntryError({ status: EntryStatus.Error })).toBeTrue();
      expect(isEntryDeleting({ status: EntryStatus.Deleting })).toBeTrue();

      expect(isEntryBusy({ status: EntryStatus.Processing })).toBeTrue();
      expect(isEntryBusy({ status: EntryStatus.Queued })).toBeTrue();
      expect(isEntryBusy({ status: EntryStatus.Ready })).toBeFalse();
      expect(isEntryBusy({ status: EntryStatus.Error })).toBeFalse();
      expect(isEntryBusy({ status: EntryStatus.Deleting })).toBeFalse();
      expect(isEntryBusy(null)).toBeFalse();
      expect(isEntryBusy(undefined)).toBeFalse();
    });

    it('enforces canEditEntry and canDeleteEntry guards consistently', () => {
      expect(canEditEntry(null)).toBeFalse();
      expect(canDeleteEntry(undefined)).toBeFalse();

      expect(canEditEntry({ status: EntryStatus.Ready })).toBeTrue();
      expect(canDeleteEntry({ status: EntryStatus.Ready })).toBeTrue();

      expect(canEditEntry({ status: EntryStatus.Error })).toBeTrue();
      expect(canDeleteEntry({ status: EntryStatus.Error })).toBeTrue();

      // Queued entries can still have metadata edited, but cannot be deleted
      expect(canEditEntry({ status: EntryStatus.Queued })).toBeTrue();
      expect(canDeleteEntry({ status: EntryStatus.Queued })).toBeFalse();

      // Processing and deleting entries cannot be edited or deleted
      expect(canEditEntry({ status: EntryStatus.Processing })).toBeFalse();
      expect(canDeleteEntry({ status: EntryStatus.Processing })).toBeFalse();

      expect(canEditEntry({ status: EntryStatus.Deleting })).toBeFalse();
      expect(canDeleteEntry({ status: EntryStatus.Deleting })).toBeFalse();
    });
  });
});
