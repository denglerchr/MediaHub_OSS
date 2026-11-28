import { FormBuilder } from '@angular/forms';
import { CustomField } from '../../core/models/database.model';
import { buildCustomFieldsFormGroup, coerceCustomFieldValues } from './custom-fields';

describe('custom-fields utilities', () => {
  const fb = new FormBuilder();

  const schema: CustomField[] = [
    { id: 1, name: 'title', type: 'TEXT' },
    { id: 2, name: 'count', type: 'INTEGER' },
    { id: 3, name: 'score', type: 'REAL' },
    { id: 4, name: 'verified', type: 'BOOLEAN' },
    { id: 5, name: 'location', type: 'COORDINATE' },
  ];

  describe('buildCustomFieldsFormGroup', () => {
    it('creates default controls when initialValues is omitted', () => {
      const group = buildCustomFieldsFormGroup(fb, schema);

      expect(group.getRawValue()).toEqual({
        title: null,
        count: null,
        score: null,
        verified: false,
        location: { latitude: null, longitude: null },
      });
      expect(group.valid).toBeTrue();
    });

    it('populates controls from initialValues including SQLite boolean 1/0', () => {
      const group = buildCustomFieldsFormGroup(fb, schema, {
        title: 'Sunset',
        count: 0,
        score: 9.5,
        verified: 1,
        location: { latitude: 48.137, longitude: 11.576 },
      });

      expect(group.getRawValue()).toEqual({
        title: 'Sunset',
        count: 0,
        score: 9.5,
        verified: true,
        location: { latitude: 48.137, longitude: 11.576 },
      });
    });

    it('attaches integer pattern validator and completeCoordinateValidator', () => {
      const group = buildCustomFieldsFormGroup(fb, schema);

      const countCtrl = group.get('count');
      countCtrl?.setValue(2.5);
      expect(countCtrl?.invalid).toBeTrue();
      countCtrl?.setValue('-12');
      expect(countCtrl?.valid).toBeTrue();

      const scoreCtrl = group.get('score');
      scoreCtrl?.setValue('Infinity');
      expect(scoreCtrl?.valid).toBeTrue();

      const locGroup = group.get('location');
      locGroup?.patchValue({ latitude: 48.1, longitude: null });
      expect(locGroup?.hasError('incompleteCoordinate')).toBeTrue();
      expect(group.invalid).toBeTrue();

      locGroup?.patchValue({ latitude: 48.1, longitude: 11.5 });
      expect(locGroup?.valid).toBeTrue();

      locGroup?.patchValue({ latitude: 95, longitude: 11.5 });
      expect(locGroup?.invalid).toBeTrue();
    });
  });

  describe('coerceCustomFieldValues', () => {
    it('omits empty optional fields when includeEmptyAsNull is false (upload/POST mode)', () => {
      const coerced = coerceCustomFieldValues(
        schema,
        {
          title: '',
          count: '',
          score: 'Infinity',
          verified: false,
          location: { latitude: null, longitude: null },
        },
        { includeEmptyAsNull: false },
      );

      expect(coerced).toEqual({
        verified: false,
      });
    });

    it('converts populated values to JSON types when includeEmptyAsNull is false', () => {
      const coerced = coerceCustomFieldValues(
        schema,
        {
          title: 'Beach',
          count: '-3',
          score: '4.25',
          verified: true,
          location: { latitude: '48.1', longitude: '11.5' },
        },
        { includeEmptyAsNull: false },
      );

      expect(coerced).toEqual({
        title: 'Beach',
        count: -3,
        score: 4.25,
        verified: true,
        location: { latitude: 48.1, longitude: 11.5 },
      });
    });

    it('emits null for empty fields when includeEmptyAsNull is true (edit/PATCH mode)', () => {
      const coerced = coerceCustomFieldValues(
        schema,
        {
          title: '',
          count: '',
          score: null,
          verified: false,
          location: { latitude: 48.1, longitude: null },
        },
        { includeEmptyAsNull: true },
      );

      expect(coerced).toEqual({
        title: null,
        count: null,
        score: null,
        verified: false,
        location: null,
      });
    });

    it('preserves zero values and skips non-finite numeric strings', () => {
      const coerced = coerceCustomFieldValues(
        schema,
        {
          title: '0',
          count: 0,
          score: 'NaN',
          verified: true,
          location: { latitude: 0, longitude: 0 },
        },
        { includeEmptyAsNull: true },
      );

      expect(coerced).toEqual({
        title: '0',
        count: 0,
        verified: true,
        location: { latitude: 0, longitude: 0 },
      });
      expect('score' in coerced).toBeFalse();
    });
  });
});
