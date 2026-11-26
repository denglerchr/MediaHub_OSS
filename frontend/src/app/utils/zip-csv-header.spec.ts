import JSZip from 'jszip';
import Papa from 'papaparse';
import { extractEntriesCsvHeaderLine } from './zip-csv-header';

describe('extractEntriesCsvHeaderLine', () => {
  const headerLine = 'id,filename,timestamp,filesize,previewsize,mime_type,status,note';
  const rows = [
    headerLine,
    '1,photo1.jpg,1700000000000,1024,128,image/jpeg,ready,hello',
    '2,photo2.jpg,1700000001000,2048,256,image/jpeg,ready,world',
  ];

  async function buildZipBlob(
    entries: Record<string, string | Uint8Array>,
    compression: 'STORE' | 'DEFLATE',
    comment?: string,
  ): Promise<Blob> {
    const zip = new JSZip();
    for (const [name, content] of Object.entries(entries)) {
      zip.file(name, content);
    }
    return zip.generateAsync({
      type: 'blob',
      compression,
      ...(comment !== undefined ? { comment } : {}),
    });
  }

  it('extracts only the header line from a STORE ZIP with multiple files and rows', async () => {
    const blob = await buildZipBlob(
      {
        'media/photo1.jpg': new Uint8Array([0xff, 0xd8, 0xff, 0xe0]),
        'entries.csv': rows.join('\n') + '\n',
        'extra.txt': 'ignored content',
      },
      'STORE',
    );

    const extracted = await extractEntriesCsvHeaderLine(blob);
    expect(extracted).toBe(headerLine + '\n');

    const parsed = Papa.parse(extracted!, { header: true, preview: 1, skipEmptyLines: true });
    expect(parsed.meta.fields).toEqual(headerLine.split(','));
  });

  it('extracts only the header line from a DEFLATE ZIP and cancels early before decompressing trailing rows', async () => {
    const manyRows = [headerLine];
    for (let i = 0; i < 5000; i++) {
      manyRows.push(`${i},photo_${i}.jpg,1700000000000,1024,128,image/jpeg,ready,payload_${i}`);
    }
    const blob = await buildZipBlob(
      {
        'media/big.bin': new Uint8Array(65536).fill(42),
        'entries.csv': manyRows.join('\r\n') + '\r\n',
      },
      'DEFLATE',
    );

    const extracted = await extractEntriesCsvHeaderLine(blob);
    expect(extracted).toBe(headerLine + '\r\n');

    const parsed = Papa.parse(extracted!, { header: true, preview: 1, skipEmptyLines: true });
    expect(parsed.meta.fields).toEqual(headerLine.split(','));
  });

  it('handles a single-line entries.csv without a trailing newline for PapaParse', async () => {
    const blob = await buildZipBlob({ 'entries.csv': headerLine }, 'DEFLATE');

    const extracted = await extractEntriesCsvHeaderLine(blob);
    expect(extracted).toBe(headerLine);

    const parsed = Papa.parse(extracted!, { header: true, preview: 1, skipEmptyLines: true });
    expect(parsed.meta.fields).toEqual(headerLine.split(','));
  });

  it('handles ZIP archive comments when locating EOCD', async () => {
    const blob = await buildZipBlob(
      { 'entries.csv': rows.join('\n') },
      'STORE',
      'Archive comment with trailing bytes',
    );

    const extracted = await extractEntriesCsvHeaderLine(blob);
    expect(extracted).toBe(headerLine + '\n');
  });

  it('returns null when entries.csv is missing or only nested in a subdirectory', async () => {
    const blob = await buildZipBlob(
      {
        'nested/entries.csv': rows.join('\n'),
        'readme.txt': 'no root csv',
      },
      'DEFLATE',
    );

    const extracted = await extractEntriesCsvHeaderLine(blob);
    expect(extracted).toBeNull();
  });

  it('rejects non-ZIP blobs', async () => {
    const badBlob = new Blob(['not a zip file at all'], { type: 'application/zip' });
    await expectAsync(extractEntriesCsvHeaderLine(badBlob)).toBeRejected();
  });
});
