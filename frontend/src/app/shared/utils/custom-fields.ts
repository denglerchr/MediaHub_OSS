import { FormBuilder, FormGroup, Validators } from '@angular/forms';
import { CustomField } from '../../core/models/database.model';
import {
  completeCoordinateValidator,
  MAX_LAT,
  MAX_LNG,
  MIN_LAT,
  MIN_LNG,
  sanitizeCoordinate,
} from './coordinates';

export const CUSTOM_FIELD_INTEGER_PATTERN = /^-?\d+$/;

function isEmptyRawValue(val: unknown): boolean {
  return (
    val === null ||
    val === undefined ||
    val === '' ||
    (typeof val === 'string' && val.trim() === '')
  );
}

/**
 * Builds a schema-driven `FormGroup` for a database's custom fields, optionally
 * populated from `initialValues`.
 */
export function buildCustomFieldsFormGroup(
  fb: FormBuilder,
  fields: readonly CustomField[],
  initialValues?: Record<string, unknown> | null,
): FormGroup {
  const group = fb.group({});

  fields.forEach((field) => {
    const hasInitial =
      initialValues !== null &&
      initialValues !== undefined &&
      Object.prototype.hasOwnProperty.call(initialValues, field.name);
    const raw = hasInitial ? initialValues[field.name] : undefined;

    if (field.type === 'COORDINATE') {
      const coordObj = raw && typeof raw === 'object' ? (raw as Record<string, unknown>) : null;
      const lat = coordObj?.['latitude'] ?? null;
      const lng = coordObj?.['longitude'] ?? null;

      group.addControl(
        field.name,
        fb.group(
          {
            latitude: [lat, [Validators.min(MIN_LAT), Validators.max(MAX_LAT)]],
            longitude: [lng, [Validators.min(MIN_LNG), Validators.max(MAX_LNG)]],
          },
          { validators: completeCoordinateValidator },
        ),
      );
    } else if (field.type === 'BOOLEAN') {
      group.addControl(field.name, fb.control(raw === 1 || raw === true));
    } else {
      const validators =
        field.type === 'INTEGER' ? [Validators.pattern(CUSTOM_FIELD_INTEGER_PATTERN)] : [];
      group.addControl(field.name, fb.control(raw ?? null, validators));
    }
  });

  return group;
}

/**
 * Coerces raw form values for custom fields into API-ready JSON types.
 *
 * - `includeEmptyAsNull: true` (PATCH): emits `null` for empty fields so cleared values reset on the server.
 * - `includeEmptyAsNull: false` (POST): omits empty optional fields.
 */
export function coerceCustomFieldValues(
  fields: readonly CustomField[],
  rawValues: Record<string, unknown>,
  options: { includeEmptyAsNull: boolean },
): Record<string, unknown> {
  const result: Record<string, unknown> = {};

  fields.forEach((field) => {
    const hasKey = Object.prototype.hasOwnProperty.call(rawValues, field.name);
    if (!hasKey && !options.includeEmptyAsNull) {
      return;
    }

    const rawValue = hasKey ? rawValues[field.name] : undefined;

    if (field.type === 'BOOLEAN') {
      result[field.name] = !!rawValue;
      return;
    }

    if (field.type === 'INTEGER' || field.type === 'REAL') {
      if (isEmptyRawValue(rawValue)) {
        if (options.includeEmptyAsNull) {
          result[field.name] = null;
        }
        return;
      }
      const num = typeof rawValue === 'boolean' ? Number.NaN : Number(rawValue);
      if (Number.isFinite(num)) {
        result[field.name] = num;
      }
      return;
    }

    if (field.type === 'COORDINATE') {
      const coord = sanitizeCoordinate(rawValue);
      if (coord !== null) {
        result[field.name] = coord;
      } else if (options.includeEmptyAsNull) {
        result[field.name] = null;
      }
      return;
    }

    if (rawValue === null || rawValue === undefined || rawValue === '') {
      if (options.includeEmptyAsNull) {
        result[field.name] = null;
      }
      return;
    }

    result[field.name] = rawValue;
  });

  return result;
}
