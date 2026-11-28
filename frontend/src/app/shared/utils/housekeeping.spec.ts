import {
  DURATION_UNIT_OPTIONS,
  HOUSEKEEPING_INTEGER_PATTERN,
  SIZE_UNIT_OPTIONS,
  formatHousekeeping,
  parseHousekeepingString,
} from './housekeeping';

describe('housekeeping utilities', () => {
  it('exports expected unit options and integer pattern', () => {
    expect(DURATION_UNIT_OPTIONS.map((o) => o.value)).toEqual(['s', 'm', 'h', 'd']);
    expect(SIZE_UNIT_OPTIONS.map((o) => o.value)).toEqual(['B', 'K', 'M', 'G', 'T']);
    expect(HOUSEKEEPING_INTEGER_PATTERN.test('0')).toBeTrue();
    expect(HOUSEKEEPING_INTEGER_PATTERN.test('25')).toBeTrue();
    expect(HOUSEKEEPING_INTEGER_PATTERN.test('-1')).toBeFalse();
    expect(HOUSEKEEPING_INTEGER_PATTERN.test('1.5')).toBeFalse();
  });

  describe('parseHousekeepingString', () => {
    it('returns zero with default unit for empty, zero, null, or undefined inputs', () => {
      expect(parseHousekeepingString('', 'duration')).toEqual({ value: 0, unit: 'h' });
      expect(parseHousekeepingString('0', 'duration', 'd')).toEqual({ value: 0, unit: 'd' });
      expect(parseHousekeepingString(null, 'size')).toEqual({ value: 0, unit: 'G' });
      expect(parseHousekeepingString(undefined, 'size', 'M')).toEqual({ value: 0, unit: 'M' });
    });

    it('parses compound and fractional backend-emitted duration strings accurately', () => {
      expect(parseHousekeepingString('1d 1h', 'duration')).toEqual({ value: 25, unit: 'h' });
      expect(parseHousekeepingString('1h 30min', 'duration')).toEqual({ value: 90, unit: 'm' });
      expect(parseHousekeepingString('48h', 'duration')).toEqual({ value: 2, unit: 'd' });
      expect(parseHousekeepingString('45s', 'duration')).toEqual({ value: 45, unit: 's' });
      expect(parseHousekeepingString('1.5h', 'duration')).toEqual({ value: 90, unit: 'm' });
    });

    it('parses fractional and aliased size strings accurately', () => {
      expect(parseHousekeepingString('1.5G', 'size')).toEqual({ value: 1536, unit: 'M' });
      expect(parseHousekeepingString('1,5GB', 'size')).toEqual({ value: 1536, unit: 'M' });
      expect(parseHousekeepingString('1024K', 'size')).toEqual({ value: 1, unit: 'M' });
      expect(parseHousekeepingString('512B', 'size')).toEqual({ value: 512, unit: 'B' });
      expect(parseHousekeepingString('2TB', 'size')).toEqual({ value: 2, unit: 'T' });
    });

    it('returns null for unparseable strings or positive sub-unit values that round to zero', () => {
      expect(parseHousekeepingString('1d 1h xyz', 'duration')).toBeNull();
      expect(parseHousekeepingString('invalid', 'size')).toBeNull();
      expect(parseHousekeepingString('0.1s', 'duration')).toBeNull();
      expect(parseHousekeepingString('0.1B', 'size')).toBeNull();
    });

    it('rejects Object.prototype property names as units and huge numeric overflows', () => {
      expect(parseHousekeepingString('5constructor', 'duration')).toBeNull();
      expect(parseHousekeepingString('5toString', 'size')).toBeNull();
      expect(parseHousekeepingString(`1${'0'.repeat(300)}P`, 'size')).toBeNull();
    });
  });

  describe('formatHousekeeping', () => {
    it('returns "0" for zero, negative, empty, null, undefined, or non-finite values', () => {
      expect(formatHousekeeping(0, 'h')).toBe('0');
      expect(formatHousekeeping(-5, 'd')).toBe('0');
      expect(formatHousekeeping('', 'G')).toBe('0');
      expect(formatHousekeeping(null, 'G')).toBe('0');
      expect(formatHousekeeping(undefined, 's')).toBe('0');
      expect(formatHousekeeping('abc', 'h')).toBe('0');
    });

    it('normalizes values into the largest evenly-divisible unit and preserves m vs M', () => {
      expect(formatHousekeeping(25, 'h')).toBe('25h');
      expect(formatHousekeeping(24, 'h')).toBe('1d');
      expect(formatHousekeeping(90, 'm')).toBe('90m');
      expect(formatHousekeeping('1.5', 'h')).toBe('90m');
      expect(formatHousekeeping(1536, 'M')).toBe('1536M');
      expect(formatHousekeeping(1024, 'M')).toBe('1G');
      expect(formatHousekeeping(1.5, 'G')).toBe('1536M');
    });

    it('returns null when a positive value rounds below 1 second or 1 byte', () => {
      expect(formatHousekeeping(0.1, 's')).toBeNull();
      expect(formatHousekeeping(0.1, 'B')).toBeNull();
    });

    it('returns null when multiplication overflows to a non-finite total', () => {
      expect(formatHousekeeping(1e300, 'P')).toBeNull();
    });
  });
});
