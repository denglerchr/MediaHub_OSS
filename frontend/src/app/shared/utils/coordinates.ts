import { AbstractControl, ValidationErrors, ValidatorFn } from '@angular/forms';
import { Coordinate } from '../../core/models/entry.model';

export const MIN_LAT = -90;
export const MAX_LAT = 90;
export const MIN_LNG = -180;
export const MAX_LNG = 180;

function isEmptyCoordPart(val: unknown): boolean {
  return val === null || val === undefined || (typeof val === 'string' && val.trim() === '');
}

function parseCoordPart(val: unknown): number {
  if (typeof val === 'number') {
    return val;
  }
  if (typeof val === 'string') {
    return Number(val);
  }
  return Number.NaN;
}

/**
 * Type guard verifying that a value is a Coordinate with finite numeric latitude and longitude.
 */
export function isCoordinate(val: unknown): val is Coordinate {
  if (val === null || val === undefined || typeof val !== 'object') {
    return false;
  }
  const candidate = val as { latitude?: unknown; longitude?: unknown };
  return (
    typeof candidate.latitude === 'number' &&
    typeof candidate.longitude === 'number' &&
    Number.isFinite(candidate.latitude) &&
    Number.isFinite(candidate.longitude)
  );
}

/**
 * Formats a coordinate as `lat.toFixed(6), lng.toFixed(6)`, or returns `fallback` if invalid.
 */
export function formatCoordinate(val: unknown, fallback = ''): string {
  if (!isCoordinate(val)) {
    return fallback;
  }
  return `${val.latitude.toFixed(6)}, ${val.longitude.toFixed(6)}`;
}

/**
 * Builds an OpenStreetMap URL for a valid coordinate, or `'#'` if invalid.
 */
export function getOpenStreetMapUrl(val: unknown): string {
  if (!isCoordinate(val)) {
    return '#';
  }
  return `https://www.openstreetmap.org/?mlat=${val.latitude}&mlon=${val.longitude}#map=16/${val.latitude}/${val.longitude}`;
}

/**
 * Parses a `{ latitude, longitude }` object, rejecting partial, non-finite, or out-of-range values.
 */
export function sanitizeCoordinate(val: unknown): Coordinate | null {
  if (!val || typeof val !== 'object') {
    return null;
  }
  const { latitude: lat, longitude: lng } = val as { latitude?: unknown; longitude?: unknown };
  if (isEmptyCoordPart(lat) || isEmptyCoordPart(lng)) {
    return null;
  }

  const numLat = parseCoordPart(lat);
  const numLng = parseCoordPart(lng);
  if (
    !Number.isFinite(numLat) ||
    !Number.isFinite(numLng) ||
    numLat < MIN_LAT ||
    numLat > MAX_LAT ||
    numLng < MIN_LNG ||
    numLng > MAX_LNG
  ) {
    return null;
  }

  return {
    latitude: numLat === 0 ? 0 : numLat,
    longitude: numLng === 0 ? 0 : numLng,
  };
}

/**
 * Reactive form group validator for `{ latitude, longitude }` pairs:
 * - Both empty is valid (optional coordinate)
 * - Only one side set yields `{ incompleteCoordinate: true }`
 * - Out-of-range or non-finite values yield `{ invalidLatitude: true }` / `{ invalidLongitude: true }`
 */
export const completeCoordinateValidator: ValidatorFn = (
  control: AbstractControl,
): ValidationErrors | null => {
  const lat = control.get('latitude')?.value;
  const lng = control.get('longitude')?.value;
  const hasLat = !isEmptyCoordPart(lat);
  const hasLng = !isEmptyCoordPart(lng);

  if (!hasLat && !hasLng) {
    return null;
  }
  if (hasLat !== hasLng) {
    return { incompleteCoordinate: true };
  }

  const numLat = parseCoordPart(lat);
  const numLng = parseCoordPart(lng);
  const errors: ValidationErrors = {};

  if (!Number.isFinite(numLat) || numLat < MIN_LAT || numLat > MAX_LAT) {
    errors['invalidLatitude'] = true;
  }
  if (!Number.isFinite(numLng) || numLng < MIN_LNG || numLng > MAX_LNG) {
    errors['invalidLongitude'] = true;
  }

  return Object.keys(errors).length > 0 ? errors : null;
};

/**
 * Reactive form validator for bounding boxes `{ min_lat, max_lat, min_lng, max_lng }`:
 * verifies all 4 fields are present, finite, within WGS84 bounds, and `min_lat <= max_lat`.
 */
export const coordinateBoxValidator: ValidatorFn = (
  control: AbstractControl,
): ValidationErrors | null => {
  const val = control.value;
  if (!val || typeof val !== 'object') {
    return { invalidBoundingBox: true };
  }
  const box = val as Record<string, unknown>;
  const keys = ['min_lat', 'max_lat', 'min_lng', 'max_lng'] as const;
  const nums: Record<(typeof keys)[number], number> = {
    min_lat: Number.NaN,
    max_lat: Number.NaN,
    min_lng: Number.NaN,
    max_lng: Number.NaN,
  };

  for (const key of keys) {
    const raw = box[key];
    if (isEmptyCoordPart(raw)) {
      return { invalidBoundingBox: true };
    }
    const num = parseCoordPart(raw);
    if (!Number.isFinite(num)) {
      return { invalidBoundingBox: true };
    }
    nums[key] = num;
  }

  if (
    nums.min_lat < MIN_LAT ||
    nums.min_lat > MAX_LAT ||
    nums.max_lat < MIN_LAT ||
    nums.max_lat > MAX_LAT ||
    nums.min_lng < MIN_LNG ||
    nums.min_lng > MAX_LNG ||
    nums.max_lng < MIN_LNG ||
    nums.max_lng > MAX_LNG
  ) {
    return { invalidBoundingBox: true };
  }

  if (nums.min_lat > nums.max_lat) {
    return { invalidBoundingBox: true };
  }

  return null;
};
