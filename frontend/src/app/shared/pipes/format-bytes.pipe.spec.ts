import { FormatBytesPipe } from './format-bytes.pipe';

describe('FormatBytesPipe', () => {
  let pipe: FormatBytesPipe;

  beforeEach(() => {
    pipe = new FormatBytesPipe();
  });

  it('returns "0 Bytes" for null, undefined, zero, negative, and non-finite inputs', () => {
    expect(pipe.transform(null)).toBe('0 Bytes');
    expect(pipe.transform(undefined)).toBe('0 Bytes');
    expect(pipe.transform(0)).toBe('0 Bytes');
    expect(pipe.transform(-1024)).toBe('0 Bytes');
    expect(pipe.transform(Number.NaN)).toBe('0 Bytes');
    expect(pipe.transform(Infinity)).toBe('0 Bytes');
    expect(pipe.transform(-Infinity)).toBe('0 Bytes');
  });

  it('formats byte values across units and clamps at TB', () => {
    expect(pipe.transform(500)).toBe('500 Bytes');
    expect(pipe.transform(1024)).toBe('1 KB');
    expect(pipe.transform(1536)).toBe('1.5 KB');
    expect(pipe.transform(1024 * 1024)).toBe('1 MB');
    expect(pipe.transform(1024 ** 4)).toBe('1 TB');
    expect(pipe.transform(1024 ** 5)).toBe('1024 TB');
  });

  it('respects custom decimal precision and clamps negative decimals to 0', () => {
    expect(pipe.transform(1234, 3)).toBe('1.205 KB');
    expect(pipe.transform(1536, 0)).toBe('2 KB');
    expect(pipe.transform(1536, -2)).toBe('2 KB');
  });
});
