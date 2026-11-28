/**
 * Formats a `Date` or epoch-ms timestamp into a local `datetime-local` input string
 * (`YYYY-MM-DDTHH:mm` or `YYYY-MM-DDTHH:mm:ss`). Returns `''` for invalid dates / `NaN`.
 */
export function toDatetimeLocal(
  input: Date | number,
  precision: 'minutes' | 'seconds' = 'seconds',
): string {
  const date = input instanceof Date ? input : new Date(input);
  if (!Number.isFinite(date.getTime())) {
    return '';
  }

  const pad = (n: number, len = 2) => String(n).padStart(len, '0');
  const year = pad(date.getFullYear(), 4);
  const month = pad(date.getMonth() + 1);
  const day = pad(date.getDate());
  const hours = pad(date.getHours());
  const minutes = pad(date.getMinutes());

  if (precision === 'minutes') {
    return `${year}-${month}-${day}T${hours}:${minutes}`;
  }

  const seconds = pad(date.getSeconds());
  return `${year}-${month}-${day}T${hours}:${minutes}:${seconds}`;
}

/**
 * Parses a local `datetime-local` string into UTC epoch milliseconds, or `null` if invalid/empty.
 * When `options.endOfMinute` is true and `value` only has minute precision (`YYYY-MM-DDTHH:mm`),
 * adds `59999` ms to include the entire final minute.
 */
export function parseDatetimeLocalToMs(
  value: string,
  options?: { endOfMinute?: boolean },
): number | null {
  if (!value || typeof value !== 'string') {
    return null;
  }
  const trimmed = value.trim();
  if (trimmed === '') {
    return null;
  }

  const ms = new Date(trimmed).getTime();
  if (!Number.isFinite(ms)) {
    return null;
  }

  if (options?.endOfMinute && /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(trimmed)) {
    return ms + 59999;
  }

  return ms;
}
