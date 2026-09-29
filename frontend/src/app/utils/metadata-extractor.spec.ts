import { extractMetadata } from './metadata-extractor';

describe('MetadataExtractor', () => {
  function createJpegWithExif(tiffPayloadBuilder: (view: DataView) => { byteLength: number }): File {
    // APP1 Header: SOI (2) + APP1 Marker (2) + Length (2) + "Exif\0\0" (6) = 12 bytes
    const buffer = new ArrayBuffer(1024);
    const view = new DataView(buffer);

    // JPEG SOI marker
    view.setUint16(0, 0xffd8);
    // APP1 marker
    view.setUint16(2, 0xffe1);

    // EXIF header
    view.setUint8(6, 0x45); // 'E'
    view.setUint8(7, 0x78); // 'x'
    view.setUint8(8, 0x69); // 'i'
    view.setUint8(9, 0x66); // 'f'
    view.setUint8(10, 0x00);
    view.setUint8(11, 0x00);

    // Build TIFF payload starting at offset 12
    const tiffView = new DataView(buffer, 12);
    const { byteLength: tiffLength } = tiffPayloadBuilder(tiffView);

    // Set APP1 length: 2 (length field itself) + 6 (Exif\0\0) + tiffLength
    view.setUint16(4, 2 + 6 + tiffLength);

    return new File([buffer], 'test-image.jpg', { type: 'image/jpeg' });
  }

  it('should extract GPS coordinates and timestamp from JPEG EXIF (Little Endian)', async () => {
    const file = createJpegWithExif((view) => {
      // Little Endian: "II" (0x4949)
      view.setUint16(0, 0x4949);
      view.setUint16(2, 0x002a, true); // 42
      view.setUint32(4, 8, true); // Offset of IFD0 is 8 bytes from TIFF start

      // IFD0 at offset 8:
      // Number of entries: 2 (ExifSubIFD + GPSInfo)
      view.setUint16(8, 2, true);

      // Entry 1: ExifSubIFD tag (0x8769)
      view.setUint16(10, 0x8769, true); // Tag
      view.setUint16(12, 4, true); // Type: LONG
      view.setUint32(14, 1, true); // Count: 1
      view.setUint32(18, 50, true); // Offset to ExifSubIFD: 50

      // Entry 2: GPSInfo tag (0x8825)
      view.setUint16(22, 0x8825, true); // Tag
      view.setUint16(24, 4, true); // Type: LONG
      view.setUint32(26, 1, true); // Count: 1
      view.setUint32(30, 100, true); // Offset to GPS SubIFD: 100

      // ExifSubIFD at offset 50:
      // Number of entries: 1 (DateTimeOriginal 0x9003)
      view.setUint16(50, 1, true);
      view.setUint16(52, 0x9003, true); // Tag
      view.setUint16(54, 2, true); // Type: ASCII
      view.setUint32(56, 20, true); // Count: 20 bytes
      view.setUint32(60, 70, true); // Offset to string: 70

      // Date string at offset 70: "2024:06:15 14:30:00\0"
      const dateStr = '2024:06:15 14:30:00\0';
      for (let i = 0; i < dateStr.length; i++) {
        view.setUint8(70 + i, dateStr.charCodeAt(i));
      }

      // GPS SubIFD at offset 100:
      // Number of entries: 4 (GPSLatitudeRef, GPSLatitude, GPSLongitudeRef, GPSLongitude)
      view.setUint16(100, 4, true);

      // Entry 1: GPSLatitudeRef (0x0001), ASCII, Count 2, Value: 'N\0'
      view.setUint16(102, 0x0001, true);
      view.setUint16(104, 2, true); // ASCII
      view.setUint32(106, 2, true); // Count 2
      view.setUint8(110, 0x4e); // 'N'
      view.setUint8(111, 0x00);

      // Entry 2: GPSLatitude (0x0002), RATIONAL, Count 3, Offset: 160
      view.setUint16(114, 0x0002, true);
      view.setUint16(116, 5, true); // RATIONAL
      view.setUint32(118, 3, true); // Count 3
      view.setUint32(122, 160, true); // Offset 160

      // Entry 3: GPSLongitudeRef (0x0003), ASCII, Count 2, Value: 'E\0'
      view.setUint16(126, 0x0003, true);
      view.setUint16(128, 2, true);
      view.setUint32(130, 2, true);
      view.setUint8(134, 0x45); // 'E'
      view.setUint8(135, 0x00);

      // Entry 4: GPSLongitude (0x0004), RATIONAL, Count 3, Offset: 184
      view.setUint16(138, 0x0004, true);
      view.setUint16(140, 5, true);
      view.setUint32(142, 3, true);
      view.setUint32(146, 184, true);

      // Latitude rationals at offset 160: 48 deg, 8 min, 13.7544 sec (48 + 8/60 + 13.7544/3600 = 48.137154)
      // Deg: 48 / 1
      view.setUint32(160, 48, true);
      view.setUint32(164, 1, true);
      // Min: 8 / 1
      view.setUint32(168, 8, true);
      view.setUint32(172, 1, true);
      // Sec: 137544 / 10000 = 13.7544
      view.setUint32(176, 137544, true);
      view.setUint32(180, 10000, true);

      // Longitude rationals at offset 184: 11 deg, 34 min, 34.0464 sec (11 + 34/60 + 34.0464/3600 = 11.576124)
      // Deg: 11 / 1
      view.setUint32(184, 11, true);
      view.setUint32(188, 1, true);
      // Min: 34 / 1
      view.setUint32(192, 34, true);
      view.setUint32(196, 1, true);
      // Sec: 340464 / 10000
      view.setUint32(200, 340464, true);
      view.setUint32(204, 10000, true);

      return { byteLength: 220 };
    });

    const result = await extractMetadata(file);
    expect(result).not.toBeNull();
    expect(result?.timestamp).toBeDefined();
    expect(result?.timestamp?.getFullYear()).toBe(2024);
    expect(result?.timestamp?.getMonth()).toBe(5); // June (0-indexed)
    expect(result?.coordinates).toBeDefined();
    expect(result?.coordinates?.latitude).toBeCloseTo(48.137154, 5);
    expect(result?.coordinates?.longitude).toBeCloseTo(11.576124, 5);
  });

  it('should handle South and West coordinates correctly with negative values', async () => {
    const file = createJpegWithExif((view) => {
      view.setUint16(0, 0x4949);
      view.setUint16(2, 0x002a, true);
      view.setUint32(4, 8, true);

      view.setUint16(8, 1, true); // 1 entry: GPSInfo
      view.setUint16(10, 0x8825, true);
      view.setUint16(12, 4, true);
      view.setUint32(14, 1, true);
      view.setUint32(18, 30, true); // Offset 30

      // GPS SubIFD at offset 30
      view.setUint16(30, 4, true);

      // Lat Ref: 'S'
      view.setUint16(32, 0x0001, true);
      view.setUint16(34, 2, true);
      view.setUint32(36, 2, true);
      view.setUint8(40, 0x53); // 'S'
      view.setUint8(41, 0x00);

      // Lat value at 90: 33 deg 51 min 54 sec (-33.865)
      view.setUint16(44, 0x0002, true);
      view.setUint16(46, 5, true);
      view.setUint32(48, 3, true);
      view.setUint32(52, 90, true);

      // Lng Ref: 'W'
      view.setUint16(56, 0x0003, true);
      view.setUint16(58, 2, true);
      view.setUint32(60, 2, true);
      view.setUint8(64, 0x57); // 'W'
      view.setUint8(65, 0x00);

      // Lng value at 114: 70 deg 40 min 12 sec (-70.67)
      view.setUint16(68, 0x0004, true);
      view.setUint16(70, 5, true);
      view.setUint32(72, 3, true);
      view.setUint32(76, 114, true);

      // Lat values: 33/1, 51/1, 54/1
      view.setUint32(90, 33, true);
      view.setUint32(94, 1, true);
      view.setUint32(98, 51, true);
      view.setUint32(102, 1, true);
      view.setUint32(106, 54, true);
      view.setUint32(110, 1, true);

      // Lng values: 70/1, 40/1, 12/1
      view.setUint32(114, 70, true);
      view.setUint32(118, 1, true);
      view.setUint32(122, 40, true);
      view.setUint32(126, 1, true);
      view.setUint32(130, 12, true);
      view.setUint32(134, 1, true);

      return { byteLength: 150 };
    });

    const result = await extractMetadata(file);
    expect(result?.coordinates).toBeDefined();
    expect(result?.coordinates?.latitude).toBeCloseTo(-33.865, 3);
    expect(result?.coordinates?.longitude).toBeCloseTo(-70.67, 2);
  });

  it('should return null coordinates if out of bounds', async () => {
    const file = createJpegWithExif((view) => {
      view.setUint16(0, 0x4949);
      view.setUint16(2, 0x002a, true);
      view.setUint32(4, 8, true);

      view.setUint16(8, 1, true);
      view.setUint16(10, 0x8825, true);
      view.setUint16(12, 4, true);
      view.setUint32(14, 1, true);
      view.setUint32(18, 30, true);

      view.setUint16(30, 4, true);

      view.setUint16(32, 0x0001, true);
      view.setUint16(34, 2, true);
      view.setUint32(36, 2, true);
      view.setUint8(40, 0x4e); // 'N'

      // Invalid latitude: 120 degrees (> 90)
      view.setUint16(44, 0x0002, true);
      view.setUint16(46, 5, true);
      view.setUint32(48, 3, true);
      view.setUint32(52, 90, true);

      view.setUint16(56, 0x0003, true);
      view.setUint16(58, 2, true);
      view.setUint32(60, 2, true);
      view.setUint8(64, 0x45); // 'E'

      view.setUint16(68, 0x0004, true);
      view.setUint16(70, 5, true);
      view.setUint32(72, 3, true);
      view.setUint32(76, 114, true);

      view.setUint32(90, 120, true);
      view.setUint32(94, 1, true);
      view.setUint32(98, 0, true);
      view.setUint32(102, 1, true);
      view.setUint32(106, 0, true);
      view.setUint32(110, 1, true);

      view.setUint32(114, 10, true);
      view.setUint32(118, 1, true);
      view.setUint32(122, 0, true);
      view.setUint32(126, 1, true);
      view.setUint32(130, 0, true);
      view.setUint32(134, 1, true);

      return { byteLength: 150 };
    });

    const result = await extractMetadata(file);
    expect(result?.coordinates).toBeUndefined();
  });
});
