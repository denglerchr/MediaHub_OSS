// frontend/src/app/utils/metadata-extractor.ts

import { Coordinate } from '../models';

export interface ExtractedMetadata {
  timestamp?: Date;
  coordinates?: Coordinate;
}

const MP4_SLICE_BYTES = 2 * 1024 * 1024;

/**
 * Extracts metadata (e.g., creation/capture timestamps and GPS coordinates) from files.
 * Supports JPEGs (EXIF) and MP4s (Movie Header Box).
 * Easily extendable to parse other formats or extract GPS coordinates, etc.
 */
export async function extractMetadata(file: File): Promise<ExtractedMetadata | null> {
  try {
    const filenameLower = file.name.toLowerCase();
    const isMp4OrQuickTime =
      file.type.startsWith('video/mp4') ||
      file.type.startsWith('video/quicktime') ||
      filenameLower.endsWith('.mp4') ||
      filenameLower.endsWith('.mov') ||
      filenameLower.endsWith('.m4v') ||
      filenameLower.endsWith('.m4a');

    if (
      file.type.startsWith('image/jpeg') ||
      filenameLower.endsWith('.jpg') ||
      filenameLower.endsWith('.jpeg')
    ) {
      // Read the first 128 KB of the image where the EXIF APP1 segment resides.
      const slice = file.slice(0, 128 * 1024);
      const buffer = await slice.arrayBuffer();
      return parseExif(buffer);
    } else if (isMp4OrQuickTime) {
      // Read the first 2 MiB of the video. If 'moov' is not at the start, check the trailing 2 MiB slice.
      const headSlice = file.slice(0, MP4_SLICE_BYTES);
      const headBuffer = await headSlice.arrayBuffer();
      let date = extractMp4Metadata(headBuffer);
      if (!date && file.size > MP4_SLICE_BYTES) {
        const tailSlice = file.slice(Math.max(0, file.size - MP4_SLICE_BYTES));
        const tailBuffer = await tailSlice.arrayBuffer();
        date = extractMp4Metadata(tailBuffer, true);
      }
      if (date) return { timestamp: date };
    }
  } catch (err) {
    console.error('[MetadataExtractor] Failed to extract metadata:', err);
  }
  return null;
}

function parseExif(buffer: ArrayBuffer): ExtractedMetadata | null {
  const view = new DataView(buffer);
  if (view.byteLength < 8) return null;

  // Check JPEG SOI marker (0xFFD8)
  if (view.getUint16(0) !== 0xffd8) return null;

  let offset = 2;
  while (offset < view.byteLength) {
    if (view.getUint8(offset) !== 0xff) break;
    // Skip consecutive 0xFF fill bytes before the marker byte
    while (offset < view.byteLength && view.getUint8(offset) === 0xff) {
      offset++;
    }
    if (offset >= view.byteLength) break;

    const marker = view.getUint8(offset);
    if (marker === 0xd9) break; // End of Image (EOI)
    if (offset + 3 > view.byteLength) break;

    const length = view.getUint16(offset + 1);
    if (length < 2 || offset + 1 + length > view.byteLength) break;

    if (marker === 0xe1) {
      // APP1 EXIF segment
      if (offset + 3 + 6 <= view.byteLength) {
        // Check EXIF header signature: "Exif\0\0"
        const isExif =
          view.getUint8(offset + 3) === 0x45 && // 'E'
          view.getUint8(offset + 4) === 0x78 && // 'x'
          view.getUint8(offset + 5) === 0x69 && // 'i'
          view.getUint8(offset + 6) === 0x66 && // 'f'
          view.getUint8(offset + 7) === 0x00 &&
          view.getUint8(offset + 8) === 0x00;

        if (isExif) {
          return parseTiff(view, offset + 9);
        }
      }
    }
    offset += 1 + length;
  }
  return null;
}

function parseTiff(view: DataView, tiffOffset: number): ExtractedMetadata | null {
  if (tiffOffset + 8 > view.byteLength) return null;

  const byteOrder = view.getUint16(tiffOffset);
  const littleEndian = byteOrder === 0x4949; // "II" (Intel Little Endian) vs "MM" (Motorola Big Endian)

  if (view.getUint16(tiffOffset + 2, littleEndian) !== 0x002a) return null; // Signature (42)

  const firstIfdOffset = view.getUint32(tiffOffset + 4, littleEndian);
  const ifdOffset = tiffOffset + firstIfdOffset;

  let exifSubIfdOffset = 0;
  let gpsSubIfdOffset = 0;
  let dateTimeStr = '';

  // Read IFD0 entries
  if (ifdOffset + 2 <= view.byteLength) {
    const numEntries = view.getUint16(ifdOffset, littleEndian);
    let entryOffset = ifdOffset + 2;
    for (let i = 0; i < numEntries; i++) {
      if (entryOffset + 12 > view.byteLength) break;
      const tag = view.getUint16(entryOffset, littleEndian);

      if (tag === 0x8769) {
        // Exif SubIFD Offset tag
        exifSubIfdOffset = view.getUint32(entryOffset + 8, littleEndian);
      } else if (tag === 0x8825) {
        // GPS Info IFD Offset tag
        gpsSubIfdOffset = view.getUint32(entryOffset + 8, littleEndian);
      } else if (tag === 0x0132) {
        // Modification Date/Time tag
        dateTimeStr = readTiffString(view, entryOffset, tiffOffset, littleEndian);
      }
      entryOffset += 12;
    }
  }

  let date: Date | null = null;

  // Read Exif SubIFD entries
  if (exifSubIfdOffset > 0) {
    const subIfdOffset = tiffOffset + exifSubIfdOffset;
    if (subIfdOffset + 2 <= view.byteLength) {
      const numEntries = view.getUint16(subIfdOffset, littleEndian);
      let entryOffset = subIfdOffset + 2;
      for (let i = 0; i < numEntries; i++) {
        if (entryOffset + 12 > view.byteLength) break;
        const tag = view.getUint16(entryOffset, littleEndian);

        if (tag === 0x9003) {
          // DateTimeOriginal (Capture time)
          const val = readTiffString(view, entryOffset, tiffOffset, littleEndian);
          if (val) {
            date = parseExifDate(val);
            break;
          }
        } else if (tag === 0x9004 && !date) {
          // DateTimeDigitized
          const val = readTiffString(view, entryOffset, tiffOffset, littleEndian);
          if (val) {
            date = parseExifDate(val);
          }
        }
        entryOffset += 12;
      }
    }
  }

  // Fallback to IFD0 modified date if DateTimeOriginal was not found
  if (!date && dateTimeStr) {
    date = parseExifDate(dateTimeStr);
  }

  let coordinates: Coordinate | null = null;
  if (gpsSubIfdOffset > 0) {
    coordinates = parseGps(view, gpsSubIfdOffset, tiffOffset, littleEndian);
  }

  if (date || coordinates) {
    const result: ExtractedMetadata = {};
    if (date) result.timestamp = date;
    if (coordinates) result.coordinates = coordinates;
    return result;
  }

  return null;
}

function parseGps(
  view: DataView,
  gpsIfdOffset: number,
  tiffOffset: number,
  littleEndian: boolean,
): Coordinate | null {
  const gpsOffset = tiffOffset + gpsIfdOffset;
  if (gpsOffset + 2 > view.byteLength) return null;

  const numEntries = view.getUint16(gpsOffset, littleEndian);
  let latRef = '';
  let lngRef = '';
  let latDegrees: number[] | null = null;
  let lngDegrees: number[] | null = null;

  let entryOffset = gpsOffset + 2;
  for (let i = 0; i < numEntries; i++) {
    if (entryOffset + 12 > view.byteLength) break;
    const tag = view.getUint16(entryOffset, littleEndian);

    if (tag === 0x0001) {
      // GPSLatitudeRef
      latRef = readTiffString(view, entryOffset, tiffOffset, littleEndian).trim().toUpperCase();
    } else if (tag === 0x0002) {
      // GPSLatitude
      latDegrees = readRationals(view, entryOffset, tiffOffset, littleEndian, 3);
    } else if (tag === 0x0003) {
      // GPSLongitudeRef
      lngRef = readTiffString(view, entryOffset, tiffOffset, littleEndian).trim().toUpperCase();
    } else if (tag === 0x0004) {
      // GPSLongitude
      lngDegrees = readRationals(view, entryOffset, tiffOffset, littleEndian, 3);
    }
    entryOffset += 12;
  }

  if (!latDegrees || !lngDegrees) return null;

  let lat = latDegrees[0] + latDegrees[1] / 60 + latDegrees[2] / 3600;
  let lng = lngDegrees[0] + lngDegrees[1] / 60 + lngDegrees[2] / 3600;

  if (latRef === 'S') lat = -lat;
  if (lngRef === 'W') lng = -lng;

  if (isNaN(lat) || isNaN(lng) || lat < -90 || lat > 90 || lng < -180 || lng > 180) {
    return null;
  }

  return {
    latitude: Number(lat.toFixed(6)),
    longitude: Number(lng.toFixed(6)),
  };
}

function readRationals(
  view: DataView,
  entryOffset: number,
  tiffOffset: number,
  littleEndian: boolean,
  expectedCount: number,
): number[] | null {
  const count = view.getUint32(entryOffset + 4, littleEndian);
  if (count < expectedCount) return null;

  const valueOffset = view.getUint32(entryOffset + 8, littleEndian);
  const actualOffset = tiffOffset + valueOffset;

  if (actualOffset + expectedCount * 8 > view.byteLength) return null;

  const values: number[] = [];
  for (let i = 0; i < expectedCount; i++) {
    const num = view.getUint32(actualOffset + i * 8, littleEndian);
    const den = view.getUint32(actualOffset + i * 8 + 4, littleEndian);
    if (den === 0) return null;
    values.push(num / den);
  }
  return values;
}

function readTiffString(
  view: DataView,
  entryOffset: number,
  tiffOffset: number,
  littleEndian: boolean,
): string {
  const count = view.getUint32(entryOffset + 4, littleEndian);
  const valueOffset = view.getUint32(entryOffset + 8, littleEndian);
  const actualOffset = count <= 4 ? entryOffset + 8 : tiffOffset + valueOffset;

  if (actualOffset + count > view.byteLength) return '';

  let str = '';
  for (let i = 0; i < count; i++) {
    const charCode = view.getUint8(actualOffset + i);
    if (charCode === 0) break; // Null terminator
    str += String.fromCharCode(charCode);
  }
  return str;
}

function parseExifDate(dateStr: string): Date | null {
  // Format: "YYYY:MM:DD HH:MM:SS"
  const match = dateStr.trim().match(/^(\d{4}):(\d{2}):(\d{2})\s+(\d{2}):(\d{2}):(\d{2})$/);
  if (!match) return null;

  const year = parseInt(match[1], 10);
  const month = parseInt(match[2], 10);
  const day = parseInt(match[3], 10);
  const hour = parseInt(match[4], 10);
  const min = parseInt(match[5], 10);
  const sec = parseInt(match[6], 10);

  if (
    year < 1900 ||
    month < 1 ||
    month > 12 ||
    day < 1 ||
    day > 31 ||
    hour < 0 ||
    hour > 23 ||
    min < 0 ||
    min > 59 ||
    sec < 0 ||
    sec > 59
  ) {
    return null;
  }

  const date = new Date(year, month - 1, day, hour, min, sec);
  if (
    isNaN(date.getTime()) ||
    date.getFullYear() !== year ||
    date.getMonth() !== month - 1 ||
    date.getDate() !== day
  ) {
    return null;
  }

  return date;
}

function readBoxHeader(
  view: DataView,
  offset: number,
  limit: number = view.byteLength,
): { type: string; headerSize: number; boxSize: number } | null {
  if (offset + 8 > limit) return null;

  const size = view.getUint32(offset);
  const type = String.fromCharCode(
    view.getUint8(offset + 4),
    view.getUint8(offset + 5),
    view.getUint8(offset + 6),
    view.getUint8(offset + 7),
  );
  const headerSize = size === 1 ? 16 : 8;

  let boxSize: number;
  if (size === 1) {
    if (offset + 16 > limit) return null;
    boxSize = Number(view.getBigUint64(offset + 8));
  } else if (size === 0) {
    boxSize = limit - offset;
  } else {
    boxSize = size;
  }

  if (boxSize < headerSize) return null;
  return { type, headerSize, boxSize };
}

function extractMp4Metadata(buffer: ArrayBuffer, scanUnaligned = false): Date | null {
  const view = new DataView(buffer);
  let offset = 0;

  while (offset + 8 <= view.byteLength) {
    const header = readBoxHeader(view, offset);
    if (!header) break;

    if (header.type === 'moov') {
      return parseMoov(
        view,
        offset + header.headerSize,
        Math.min(offset + header.boxSize, view.byteLength),
      );
    }

    offset += header.boxSize;
  }

  // Trailing slices of non-faststart files often start mid-mdat rather than on a box boundary.
  if (scanUnaligned) {
    for (let i = 4; i <= view.byteLength - 4; i++) {
      if (
        view.getUint8(i) === 0x6d && // 'm'
        view.getUint8(i + 1) === 0x6f && // 'o'
        view.getUint8(i + 2) === 0x6f && // 'o'
        view.getUint8(i + 3) === 0x76 // 'v'
      ) {
        const boxStart = i - 4;
        const header = readBoxHeader(view, boxStart);
        if (header && header.type === 'moov') {
          const date = parseMoov(
            view,
            boxStart + header.headerSize,
            Math.min(boxStart + header.boxSize, view.byteLength),
          );
          if (date) return date;
        }
      }
    }
  }

  return null;
}

function parseMoov(view: DataView, start: number, end: number): Date | null {
  const limit = Math.min(end, view.byteLength);
  let offset = start;
  while (offset + 8 <= limit) {
    const header = readBoxHeader(view, offset, limit);
    if (!header) break;

    if (header.type === 'mvhd') {
      // Movie Header Box
      if (offset + header.headerSize + 16 <= limit) {
        const version = view.getUint8(offset + header.headerSize);
        let creationTimeSec = 0;

        if (version === 1) {
          creationTimeSec = Number(view.getBigUint64(offset + header.headerSize + 4));
        } else {
          creationTimeSec = view.getUint32(offset + header.headerSize + 4);
        }

        if (creationTimeSec > 0) {
          // Seconds from Jan 1, 1904 to Jan 1, 1970
          const secondsFrom1904To1970 = 2082844800;
          const unixTimestampMs = (creationTimeSec - secondsFrom1904To1970) * 1000;
          const date = new Date(unixTimestampMs);

          if (Number.isFinite(date.getTime())) {
            return date;
          }
        }
      }
    }

    offset += header.boxSize;
  }
  return null;
}
