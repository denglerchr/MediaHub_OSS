export type HousekeepingKind = 'duration' | 'size';

export interface HousekeepingUnitOption {
  readonly value: string;
  readonly label: string;
}

export interface ParsedHousekeeping {
  value: number;
  unit: string;
}

export const HOUSEKEEPING_INTEGER_PATTERN = /^\d+$/;

export const DURATION_UNIT_OPTIONS: readonly HousekeepingUnitOption[] = [
  { value: 's', label: 'Seconds' },
  { value: 'm', label: 'Minutes' },
  { value: 'h', label: 'Hours' },
  { value: 'd', label: 'Days' },
];

export const SIZE_UNIT_OPTIONS: readonly HousekeepingUnitOption[] = [
  { value: 'B', label: 'Bytes' },
  { value: 'K', label: 'KB' },
  { value: 'M', label: 'MB' },
  { value: 'G', label: 'GB' },
  { value: 'T', label: 'TB' },
];

// Unit factors in the smallest unit of each kind (seconds / bytes), matching the
// units understood by the backend (internal/shared/parsers.go).
const DURATION_FACTORS: Record<string, number> = {
  s: 1,
  m: 60,
  h: 3600,
  d: 86400,
};

const SIZE_FACTORS: Record<string, number> = {
  b: 1,
  k: 1024,
  m: 1024 ** 2,
  g: 1024 ** 3,
  t: 1024 ** 4,
  p: 1024 ** 5,
};

const DURATION_ALIASES: Record<string, string> = {
  s: 's',
  sec: 's',
  secs: 's',
  second: 's',
  seconds: 's',
  m: 'm',
  min: 'm',
  mins: 'm',
  minute: 'm',
  minutes: 'm',
  h: 'h',
  hr: 'h',
  hrs: 'h',
  hour: 'h',
  hours: 'h',
  d: 'd',
  day: 'd',
  days: 'd',
};

const SIZE_ALIASES: Record<string, string> = {
  b: 'b',
  byte: 'b',
  bytes: 'b',
  k: 'k',
  kb: 'k',
  m: 'm',
  mb: 'm',
  g: 'g',
  gb: 'g',
  t: 't',
  tb: 't',
  p: 'p',
  pb: 'p',
};

const DURATION_DISPLAY_UNITS = ['d', 'h', 'm', 's'] as const;
const SIZE_DISPLAY_UNITS = ['T', 'G', 'M', 'K', 'B'] as const;

function inferHousekeepingKind(unit: string): HousekeepingKind | null {
  if (unit === 'M') {
    return 'size';
  }
  if (unit === 'm') {
    return 'duration';
  }
  const lower = unit.toLowerCase();
  if (Object.prototype.hasOwnProperty.call(DURATION_ALIASES, lower)) {
    return 'duration';
  }
  if (Object.prototype.hasOwnProperty.call(SIZE_ALIASES, lower)) {
    return 'size';
  }
  return null;
}

/**
 * Parses housekeeping strings such as `"100G"`, `"24h"`, `"1d 1h"`, `"1h 30min"`, or `"1.5G"`
 * into `{ value, unit }` in the largest evenly-divisible display unit.
 * Returns `null` when a non-empty value cannot be parsed or rounds to zero.
 */
export function parseHousekeepingString(
  valueStr: string | number | null | undefined,
  type: HousekeepingKind,
  defaultUnit?: string,
): ParsedHousekeeping | null {
  const fallbackUnit = defaultUnit ?? (type === 'duration' ? 'h' : 'G');

  if (valueStr === undefined || valueStr === null) {
    return { value: 0, unit: fallbackUnit };
  }

  const str = String(valueStr).trim();
  if (str === '' || str === '0') {
    return { value: 0, unit: fallbackUnit };
  }

  const factors = type === 'duration' ? DURATION_FACTORS : SIZE_FACTORS;
  const aliases = type === 'duration' ? DURATION_ALIASES : SIZE_ALIASES;
  const displayUnits = type === 'duration' ? DURATION_DISPLAY_UNITS : SIZE_DISPLAY_UNITS;

  let rest = str;
  let total = 0;
  let sawPositiveNumber = false;

  while (rest.length > 0) {
    const match = rest.match(/^(\d+(?:[.,]\d+)?)\s*([a-zA-Z]*)/);
    if (!match) {
      return null;
    }
    const rawUnit = (match[2] || fallbackUnit).toLowerCase();
    const canonical = Object.prototype.hasOwnProperty.call(aliases, rawUnit)
      ? aliases[rawUnit]
      : undefined;
    if (!canonical) {
      return null;
    }
    const num = parseFloat(match[1].replace(',', '.'));
    if (!Number.isFinite(num)) {
      return null;
    }
    sawPositiveNumber = sawPositiveNumber || num > 0;
    total += num * factors[canonical];
    rest = rest.slice(match[0].length).trim();
  }

  const baseTotal = Math.round(total);
  if (!Number.isFinite(baseTotal)) {
    return null;
  }
  if (baseTotal === 0) {
    return sawPositiveNumber ? null : { value: 0, unit: fallbackUnit };
  }

  for (const unit of displayUnits) {
    const factor = factors[unit.toLowerCase()];
    if (baseTotal % factor === 0) {
      return { value: baseTotal / factor, unit };
    }
  }

  return { value: baseTotal, unit: displayUnits[displayUnits.length - 1] };
}

/**
 * Formats a form value + unit into the normalized integer-unit string expected by the backend
 * (e.g. `"25h"`, `"1536M"`, `"90m"`, or `"0"` when disabled). Returns `null` when a positive
 * value is smaller than one whole second or byte.
 */
export function formatHousekeeping(
  value: number | string | null | undefined,
  unit: string,
  type?: HousekeepingKind,
): string | null {
  if (value === null || value === undefined || value === '') {
    return '0';
  }
  const numValue = typeof value === 'number' ? value : Number(String(value).trim());
  if (!Number.isFinite(numValue) || numValue <= 0) {
    return '0';
  }

  const kind = type ?? inferHousekeepingKind(unit);
  if (!kind) {
    const rounded = Math.round(numValue);
    return rounded <= 0 ? null : `${rounded}${unit}`;
  }

  const factors = kind === 'duration' ? DURATION_FACTORS : SIZE_FACTORS;
  const aliases = kind === 'duration' ? DURATION_ALIASES : SIZE_ALIASES;
  const displayUnits = kind === 'duration' ? DURATION_DISPLAY_UNITS : SIZE_DISPLAY_UNITS;

  const normalizedUnitKey = unit.toLowerCase();
  const canonical = Object.prototype.hasOwnProperty.call(aliases, normalizedUnitKey)
    ? aliases[normalizedUnitKey]
    : undefined;
  const factor = canonical ? (factors[canonical] ?? 1) : 1;
  const baseTotal = Math.round(numValue * factor);
  if (!Number.isFinite(baseTotal) || baseTotal <= 0) {
    return null;
  }

  for (const displayUnit of displayUnits) {
    const displayFactor = factors[displayUnit.toLowerCase()];
    if (baseTotal % displayFactor === 0) {
      const normalizedUnit = kind === 'duration' ? displayUnit.toLowerCase() : displayUnit;
      return `${baseTotal / displayFactor}${normalizedUnit}`;
    }
  }

  const smallestUnit = displayUnits[displayUnits.length - 1];
  return `${baseTotal}${kind === 'duration' ? smallestUnit.toLowerCase() : smallestUnit}`;
}
