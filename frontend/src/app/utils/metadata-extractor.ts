// frontend/src/app/utils/metadata-extractor.ts

import { Coordinate } from '../models';

export interface ExtractedMetadata {
  timestamp?: Date;
  coordinates?: Coordinate;
}

/**
 * Extracts metadata (e.g., creation/capture timestamps and GPS coordinates) from files.
 * Supports JPEGs (EXIF) and MP4s (Movie Header Box).
 * Easily extendable to parse other formats or extract GPS coordinates, etc.
 */
export async function extractMetadata(file: File): Promise<ExtractedMetadata | null> {
  try {
    const filenameLower = file.name.toLowerCase();
    if (file.type.startsWith('image/jpeg') || filenameLower.endsWith('.jpg') || filenameLower.endsWith('.jpeg')) {
      // Read the first 128 KB of the image where the EXIF APP1 segment resides.
      const slice = file.slice(0, 128 * 1024);
      const buffer = await slice.arrayBuffer();
      return parseExif(buffer);
    } else if (file.type.startsWith('video/mp4') || filenameLower.endsWith('.mp4')) {
      // Read the first 2 MB of the video. The 'moov' header box is typically at the start or near the end.
      const slice = file.slice(0, 2 * 1024 * 1024);
      const buffer = await slice.arrayBuffer();
      const date = parseMp4(buffer);
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
  if (view.getUint16(0) !== 0xFFD8) return null;

  let offset = 2;
  while (offset < view.byteLength) {
    if (offset + 4 > view.byteLength) break;
    if (view.getUint8(offset) !== 0xFF) break;
    
    const marker = view.getUint8(offset + 1);
    if (marker === 0xD9) break; // End of Image (EOI)
    
    const length = view.getUint16(offset + 2);
    if (offset + 2 + length > view.byteLength) break;

    if (marker === 0xE1) { // APP1 EXIF segment
      if (offset + 4 + 6 <= view.byteLength) {
        // Check EXIF header signature: "Exif\0\0"
        const isExif =
          view.getUint8(offset + 4) === 0x45 && // 'E'
          view.getUint8(offset + 5) === 0x78 && // 'x'
          view.getUint8(offset + 6) === 0x69 && // 'i'
          view.getUint8(offset + 7) === 0x66 && // 'f'
          view.getUint8(offset + 8) === 0x00 &&
          view.getUint8(offset + 9) === 0x00;

        if (isExif) {
          return parseTiff(view, offset + 10);
        }
      }
    }
    offset += 2 + length;
  }
  return null;
}

function parseTiff(view: DataView, tiffOffset: number): ExtractedMetadata | null {
  if (tiffOffset + 8 > view.byteLength) return null;
  
  const byteOrder = view.getUint16(tiffOffset);
  const littleEndian = byteOrder === 0x4949; // "II" (Intel Little Endian) vs "MM" (Motorola Big Endian)
  
  if (view.getUint16(tiffOffset + 2, littleEndian) !== 0x002A) return null; // Signature (42)

  const firstIfdOffset = view.getUint32(tiffOffset + 4, littleEndian);
  let ifdOffset = tiffOffset + firstIfdOffset;

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
      
      if (tag === 0x8769) { // Exif SubIFD Offset tag
        exifSubIfdOffset = view.getUint32(entryOffset + 8, littleEndian);
      } else if (tag === 0x8825) { // GPS Info IFD Offset tag
        gpsSubIfdOffset = view.getUint32(entryOffset + 8, littleEndian);
      } else if (tag === 0x0132) { // Modification Date/Time tag
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
        
        if (tag === 0x9003) { // DateTimeOriginal (Capture time)
          const val = readTiffString(view, entryOffset, tiffOffset, littleEndian);
          if (val) {
            date = parseExifDate(val);
            break;
          }
        } else if (tag === 0x9004 && !date) { // DateTimeDigitized
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

function parseGps(view: DataView, gpsIfdOffset: number, tiffOffset: number, littleEndian: boolean): Coordinate | null {
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

    if (tag === 0x0001) { // GPSLatitudeRef
      latRef = readTiffString(view, entryOffset, tiffOffset, littleEndian).trim().toUpperCase();
    } else if (tag === 0x0002) { // GPSLatitude
      latDegrees = readRationals(view, entryOffset, tiffOffset, littleEndian, 3);
    } else if (tag === 0x0003) { // GPSLongitudeRef
      lngRef = readTiffString(view, entryOffset, tiffOffset, littleEndian).trim().toUpperCase();
    } else if (tag === 0x0004) { // GPSLongitude
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
    longitude: Number(lng.toFixed(6))
  };
}

function readRationals(
  view: DataView,
  entryOffset: number,
  tiffOffset: number,
  littleEndian: boolean,
  expectedCount: number
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
    values.push(den !== 0 ? num / den : 0);
  }
  return values;
}

function readTiffString(view: DataView, entryOffset: number, tiffOffset: number, littleEndian: boolean): string {
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
  if (match) {
    const [_, y, m, d, hh, mm, ss] = match;
    const date = new Date(parseInt(y, 10), parseInt(m, 10) - 1, parseInt(d, 10), parseInt(hh, 10), parseInt(mm, 10), parseInt(ss, 10));
    if (!isNaN(date.getTime())) {
      return date;
    }
  }
  return null;
}

function parseMp4(buffer: ArrayBuffer): Date | null {
  const view = new DataView(buffer);
  let offset = 0;
  
  while (offset + 8 <= view.byteLength) {
    const size = view.getUint32(offset);
    const type = String.fromCharCode(
      view.getUint8(offset + 4),
      view.getUint8(offset + 5),
      view.getUint8(offset + 6),
      view.getUint8(offset + 7)
    );

    const headerSize = size === 1 ? 16 : 8;
    const boxSize = size === 1 ? Number(view.getBigUint64(offset + 8)) : size;

    if (type === 'moov') {
      return parseMoov(view, offset + headerSize, offset + boxSize);
    }

    if (boxSize <= 0) break;
    offset += boxSize;
  }
  return null;
}

function parseMoov(view: DataView, start: number, end: number): Date | null {
  let offset = start;
  while (offset + 8 <= end && offset + 8 <= view.byteLength) {
    const size = view.getUint32(offset);
    const type = String.fromCharCode(
      view.getUint8(offset + 4),
      view.getUint8(offset + 5),
      view.getUint8(offset + 6),
      view.getUint8(offset + 7)
    );
    const headerSize = size === 1 ? 16 : 8;
    const boxSize = size === 1 ? Number(view.getBigUint64(offset + 8)) : size;

    if (type === 'mvhd') { // Movie Header Box
      if (offset + headerSize + 16 <= view.byteLength) {
        const version = view.getUint8(offset + headerSize);
        let creationTimeSec = 0;
        
        if (version === 1) {
          creationTimeSec = Number(view.getBigUint64(offset + headerSize + 4));
        } else {
          creationTimeSec = view.getUint32(offset + headerSize + 4);
        }

        if (creationTimeSec > 0) {
          // Seconds from Jan 1, 1904 to Jan 1, 1970
          const secondsFrom1904To1970 = 2082844800;
          const unixTimestampMs = (creationTimeSec - secondsFrom1904To1970) * 1000;
          const date = new Date(unixTimestampMs);
          
          if (!isNaN(date.getTime()) && date.getFullYear() > 1980 && date.getFullYear() < 2100) {
            return date;
          }
        }
      }
    }
    
    if (boxSize <= 0) break;
    offset += boxSize;
  }
  return null;
}
