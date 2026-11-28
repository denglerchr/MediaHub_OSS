import { FormControl } from '@angular/forms';
import {
  CUSTOM_FIELD_NAME_MAX_LENGTH,
  CUSTOM_FIELD_NAME_PATTERN,
  DATABASE_NAME_MAX_LENGTH,
  DATABASE_NAME_PATTERN,
  RESERVED_CUSTOM_FIELD_NAMES,
  isReservedCustomFieldName,
  isValidCustomFieldName,
  reservedCustomFieldNameValidator,
  validateCustomFieldName,
} from './validation';

describe('validation utilities', () => {
  it('exports expected name patterns, max lengths, and reserved names', () => {
    expect(CUSTOM_FIELD_NAME_MAX_LENGTH).toBe(64);
    expect(DATABASE_NAME_MAX_LENGTH).toBe(64);
    expect(CUSTOM_FIELD_NAME_PATTERN.test('_field_1')).toBeTrue();
    expect(DATABASE_NAME_PATTERN.test('my_db')).toBeTrue();
    expect(RESERVED_CUSTOM_FIELD_NAMES.has('timestamp')).toBeTrue();
    expect(RESERVED_CUSTOM_FIELD_NAMES.has('preview_filesize')).toBeTrue();
  });

  it('isValidCustomFieldName and isReservedCustomFieldName enforce rules', () => {
    expect(isValidCustomFieldName('artist')).toBeTrue();
    expect(isValidCustomFieldName('1artist')).toBeFalse();
    expect(isValidCustomFieldName('my-field')).toBeFalse();

    expect(isReservedCustomFieldName('WIDTH')).toBeTrue();
    expect(isReservedCustomFieldName(' filename ')).toBeTrue();
    expect(isReservedCustomFieldName('custom_tag')).toBeFalse();
  });

  it('reservedCustomFieldNameValidator returns { reserved: true } for reserved names', () => {
    expect(reservedCustomFieldNameValidator(new FormControl('duration'))).toEqual({
      reserved: true,
    });
    expect(reservedCustomFieldNameValidator(new FormControl('album'))).toBeNull();
  });

  describe('validateCustomFieldName', () => {
    const existing = ['Artist', 'rating'];

    it('returns null for valid unique names', () => {
      expect(validateCustomFieldName('album_name', existing)).toBeNull();
      expect(validateCustomFieldName('_meta2', existing)).toBeNull();
    });

    it('rejects empty or whitespace-only names', () => {
      expect(validateCustomFieldName('', existing)).toBe('Field name is required.');
      expect(validateCustomFieldName('   ', existing)).toBe('Field name is required.');
    });

    it('rejects names exceeding 64 characters', () => {
      const longName = 'a'.repeat(CUSTOM_FIELD_NAME_MAX_LENGTH + 1);
      expect(validateCustomFieldName(longName, existing)).toBe(
        `Field name cannot exceed ${CUSTOM_FIELD_NAME_MAX_LENGTH} characters.`,
      );
      expect(validateCustomFieldName('a'.repeat(64), existing)).toBeNull();
    });

    it('rejects invalid identifier syntax', () => {
      expect(validateCustomFieldName('9lives', existing)).toContain('must start with a letter');
      expect(validateCustomFieldName('bad-name', existing)).toContain('must start with a letter');
    });

    it('rejects reserved standard/media names regardless of case', () => {
      expect(validateCustomFieldName('Timestamp', existing)).toContain('is reserved');
      expect(validateCustomFieldName('width', existing)).toContain('is reserved');
    });

    it('rejects case-insensitive duplicates unless ignored via options.ignoreName', () => {
      expect(validateCustomFieldName('artist', existing)).toContain('already exists');
      expect(validateCustomFieldName('RATING', existing)).toContain('already exists');

      expect(validateCustomFieldName('artist', existing, { ignoreName: 'Artist' })).toBeNull();
      expect(validateCustomFieldName('Artist', existing, { ignoreName: 'artist' })).toBeNull();
      expect(validateCustomFieldName('rating', existing, { ignoreName: 'Artist' })).toContain(
        'already exists',
      );
    });
  });
});
