import { parseDatetimeLocalToMs, toDatetimeLocal } from './datetime';

describe('datetime utilities', () => {
  describe('toDatetimeLocal', () => {
    it('formats local date/time at second precision by default and minute precision when requested', () => {
      const localDate = new Date(2026, 9, 6, 14, 5, 9);
      expect(toDatetimeLocal(localDate)).toBe('2026-10-06T14:05:09');
      expect(toDatetimeLocal(localDate, 'seconds')).toBe('2026-10-06T14:05:09');
      expect(toDatetimeLocal(localDate, 'minutes')).toBe('2026-10-06T14:05');
      expect(toDatetimeLocal(localDate.getTime(), 'minutes')).toBe('2026-10-06T14:05');
    });

    it('handles pre-1970 timestamps cleanly', () => {
      const vintage = new Date(1950, 5, 15, 10, 30, 45);
      expect(toDatetimeLocal(vintage.getTime())).toBe('1950-06-15T10:30:45');
    });

    it('returns empty string for invalid Date or NaN', () => {
      expect(toDatetimeLocal(new Date('invalid'))).toBe('');
      expect(toDatetimeLocal(Number.NaN)).toBe('');
      expect(toDatetimeLocal(Infinity)).toBe('');
    });
  });

  describe('parseDatetimeLocalToMs', () => {
    it('parses datetime-local strings into epoch ms', () => {
      const str = '2026-10-06T14:05:09';
      expect(parseDatetimeLocalToMs(str)).toBe(new Date(str).getTime());

      const minStr = '2026-10-06T14:05';
      expect(parseDatetimeLocalToMs(minStr)).toBe(new Date(minStr).getTime());
    });

    it('adds 59999ms only when endOfMinute is true and input has minute precision', () => {
      const minStr = '2026-10-06T14:05';
      expect(parseDatetimeLocalToMs(minStr, { endOfMinute: true })).toBe(
        new Date(minStr).getTime() + 59999,
      );

      const secStr = '2026-10-06T14:05:10';
      expect(parseDatetimeLocalToMs(secStr, { endOfMinute: true })).toBe(
        new Date(secStr).getTime(),
      );
    });

    it('returns null for empty, whitespace, or invalid strings', () => {
      expect(parseDatetimeLocalToMs('')).toBeNull();
      expect(parseDatetimeLocalToMs('   ')).toBeNull();
      expect(parseDatetimeLocalToMs('not-a-date')).toBeNull();
    });
  });
});
