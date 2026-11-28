import { FormControl, FormGroup } from '@angular/forms';
import {
  MAX_LAT,
  MAX_LNG,
  MIN_LAT,
  MIN_LNG,
  completeCoordinateValidator,
  coordinateBoxValidator,
  formatCoordinate,
  getOpenStreetMapUrl,
  isCoordinate,
  sanitizeCoordinate,
} from './coordinates';

describe('coordinates utilities', () => {
  describe('constants', () => {
    it('defines standard WGS84 coordinate bounds', () => {
      expect(MIN_LAT).toBe(-90);
      expect(MAX_LAT).toBe(90);
      expect(MIN_LNG).toBe(-180);
      expect(MAX_LNG).toBe(180);
    });
  });

  describe('isCoordinate', () => {
    it('returns true for objects with finite numeric latitude and longitude', () => {
      expect(isCoordinate({ latitude: 48.137154, longitude: 11.576124 })).toBeTrue();
      expect(isCoordinate({ latitude: 0, longitude: 0 })).toBeTrue();
      expect(isCoordinate({ latitude: -90, longitude: 180 })).toBeTrue();
    });

    it('returns false for non-objects, partial objects, strings, and non-finite numbers', () => {
      expect(isCoordinate(null)).toBeFalse();
      expect(isCoordinate(undefined)).toBeFalse();
      expect(isCoordinate('48.1, 11.5')).toBeFalse();
      expect(isCoordinate({ latitude: 48.1 })).toBeFalse();
      expect(isCoordinate({ latitude: '48.1', longitude: '11.5' })).toBeFalse();
      expect(isCoordinate({ latitude: Number.NaN, longitude: 11.5 })).toBeFalse();
      expect(isCoordinate({ latitude: 48.1, longitude: Infinity })).toBeFalse();
      expect(isCoordinate({ latitude: -Infinity, longitude: 0 })).toBeFalse();
    });
  });

  describe('formatCoordinate', () => {
    it('formats valid coordinates to 6 decimal places', () => {
      expect(formatCoordinate({ latitude: 48.1371541, longitude: 11.5 })).toBe(
        '48.137154, 11.500000',
      );
      expect(formatCoordinate({ latitude: 0, longitude: 0 })).toBe('0.000000, 0.000000');
    });

    it('returns the fallback string when invalid', () => {
      expect(formatCoordinate(null)).toBe('');
      expect(formatCoordinate(undefined, 'N/A')).toBe('N/A');
      expect(formatCoordinate({ latitude: Number.NaN, longitude: 0 }, 'N/A')).toBe('N/A');
    });
  });

  describe('getOpenStreetMapUrl', () => {
    it('builds the expected OpenStreetMap URL for valid coordinates', () => {
      expect(getOpenStreetMapUrl({ latitude: 48.137154, longitude: 11.576124 })).toBe(
        'https://www.openstreetmap.org/?mlat=48.137154&mlon=11.576124#map=16/48.137154/11.576124',
      );
    });

    it('returns "#" for invalid coordinates', () => {
      expect(getOpenStreetMapUrl(null)).toBe('#');
      expect(getOpenStreetMapUrl({ latitude: Number.NaN, longitude: 10 })).toBe('#');
    });
  });

  describe('sanitizeCoordinate', () => {
    it('parses numeric and string coordinate pairs within bounds', () => {
      expect(sanitizeCoordinate({ latitude: 48.1, longitude: 11.5 })).toEqual({
        latitude: 48.1,
        longitude: 11.5,
      });
      expect(sanitizeCoordinate({ latitude: '0', longitude: '-180' })).toEqual({
        latitude: 0,
        longitude: -180,
      });
      expect(sanitizeCoordinate({ latitude: -90, longitude: 180 })).toEqual({
        latitude: -90,
        longitude: 180,
      });
    });

    it('returns null for partial, empty, non-finite, or out-of-range coordinates', () => {
      expect(sanitizeCoordinate(null)).toBeNull();
      expect(sanitizeCoordinate('invalid')).toBeNull();
      expect(sanitizeCoordinate({ latitude: 48.1, longitude: null })).toBeNull();
      expect(sanitizeCoordinate({ latitude: '', longitude: 11.5 })).toBeNull();
      expect(sanitizeCoordinate({ latitude: '   ', longitude: '11.5' })).toBeNull();
      expect(sanitizeCoordinate({ latitude: true, longitude: 11.5 })).toBeNull();
      expect(sanitizeCoordinate({ latitude: Number.NaN, longitude: 11.5 })).toBeNull();
      expect(sanitizeCoordinate({ latitude: 48.1, longitude: Infinity })).toBeNull();
      expect(sanitizeCoordinate({ latitude: 90.01, longitude: 0 })).toBeNull();
      expect(sanitizeCoordinate({ latitude: -90.01, longitude: 0 })).toBeNull();
      expect(sanitizeCoordinate({ latitude: 0, longitude: 180.01 })).toBeNull();
      expect(sanitizeCoordinate({ latitude: 0, longitude: -180.01 })).toBeNull();
    });
  });

  describe('completeCoordinateValidator', () => {
    function makeCoordGroup(lat: unknown, lng: unknown): FormGroup {
      return new FormGroup(
        {
          latitude: new FormControl(lat),
          longitude: new FormControl(lng),
        },
        { validators: completeCoordinateValidator },
      );
    }

    it('allows both empty values and valid complete pairs', () => {
      expect(makeCoordGroup(null, null).valid).toBeTrue();
      expect(makeCoordGroup('', '').valid).toBeTrue();
      expect(makeCoordGroup(0, 0).valid).toBeTrue();
      expect(makeCoordGroup(-90, 180).valid).toBeTrue();
      expect(makeCoordGroup('48.1', '11.5').valid).toBeTrue();
    });

    it('flags incomplete pairs when only one field is populated', () => {
      const onlyLat = makeCoordGroup(48.1, null);
      expect(onlyLat.errors).toEqual({ incompleteCoordinate: true });

      const onlyLng = makeCoordGroup('', 11.5);
      expect(onlyLng.errors).toEqual({ incompleteCoordinate: true });
    });

    it('flags out-of-range or non-finite latitude and longitude', () => {
      expect(makeCoordGroup(91, 10).errors).toEqual({ invalidLatitude: true });
      expect(makeCoordGroup(-91, 10).errors).toEqual({ invalidLatitude: true });
      expect(makeCoordGroup('abc', 10).errors).toEqual({ invalidLatitude: true });
      expect(makeCoordGroup(Infinity, 10).errors).toEqual({ invalidLatitude: true });

      expect(makeCoordGroup(10, 181).errors).toEqual({ invalidLongitude: true });
      expect(makeCoordGroup(10, -181).errors).toEqual({ invalidLongitude: true });
      expect(makeCoordGroup(10, Number.NaN).errors).toEqual({ invalidLongitude: true });

      expect(makeCoordGroup(95, 200).errors).toEqual({
        invalidLatitude: true,
        invalidLongitude: true,
      });
    });
  });

  describe('coordinateBoxValidator', () => {
    it('accepts valid bounding boxes including antimeridian wrap (min_lng > max_lng)', () => {
      const valid = new FormControl(
        { min_lat: -10, max_lat: 45, min_lng: -120, max_lng: 20 },
        coordinateBoxValidator,
      );
      expect(valid.valid).toBeTrue();

      const antimeridian = new FormControl(
        { min_lat: 10, max_lat: 20, min_lng: 170, max_lng: -170 },
        coordinateBoxValidator,
      );
      expect(antimeridian.valid).toBeTrue();

      const equalBounds = new FormControl(
        { min_lat: 0, max_lat: 0, min_lng: 0, max_lng: 0 },
        coordinateBoxValidator,
      );
      expect(equalBounds.valid).toBeTrue();
    });

    it('rejects missing, non-finite, out-of-bounds, or inverted latitude boxes', () => {
      const invalidCases = [
        null,
        'not-an-object',
        { min_lat: '', max_lat: 10, min_lng: 0, max_lng: 10 },
        { min_lat: 0, max_lat: null, min_lng: 0, max_lng: 10 },
        { min_lat: 0, max_lat: 10, min_lng: 'abc', max_lng: 10 },
        { min_lat: 0, max_lat: 10, min_lng: 0, max_lng: Infinity },
        { min_lat: -91, max_lat: 10, min_lng: 0, max_lng: 10 },
        { min_lat: 0, max_lat: 91, min_lng: 0, max_lng: 10 },
        { min_lat: 0, max_lat: 10, min_lng: -181, max_lng: 10 },
        { min_lat: 0, max_lat: 10, min_lng: 0, max_lng: 181 },
        { min_lat: 20, max_lat: 10, min_lng: 0, max_lng: 10 },
      ];

      invalidCases.forEach((box) => {
        const ctrl = new FormControl(box, coordinateBoxValidator);
        expect(ctrl.errors).toEqual({ invalidBoundingBox: true });
      });
    });
  });
});
