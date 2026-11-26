// Streaming ZIP Central Directory + first-line reader for `entries.csv`.
// Inspects only the trailing EOCD, Central Directory, Local File Header, and
// the first header line of `entries.csv` via `Blob.slice()` so large import
// archives do not get loaded or decompressed into memory.

const EOCD_SIGNATURE = 0x06054b50;
const ZIP64_LOCATOR_SIGNATURE = 0x07064b50;
const ZIP64_EOCD_SIGNATURE = 0x06064b50;
const CD_SIGNATURE = 0x02014b50;
const LFH_SIGNATURE = 0x04034b50;
const ZIP64_EXTRA_HEADER_ID = 0x0001;

const EOCD_MIN_SIZE = 22;
const MAX_ZIP_COMMENT_BYTES = 65535;
const MAX_EOCD_SEARCH_BYTES = EOCD_MIN_SIZE + MAX_ZIP_COMMENT_BYTES; // 65557
const ZIP64_LOCATOR_SIZE = 20;
const ZIP64_EOCD_MIN_SIZE = 56;
const CD_FIXED_HEADER_SIZE = 46;
const LFH_FIXED_HEADER_SIZE = 30;

const MAX_CD_BYTES = 32 * 1024 * 1024;
const MAX_JSZIP_FALLBACK_BYTES = 50 * 1024 * 1024;

interface ZipEntryLocation {
  compressionMethod: number;
  flags: number;
  compressedSize: number;
  uncompressedSize: number;
  localHeaderOffset: number;
}

function toSafeNumber(value: bigint): number {
  if (value > BigInt(Number.MAX_SAFE_INTEGER)) {
    throw new Error('ZIP archive offsets exceed safe integer range.');
  }
  return Number(value);
}

function sliceFirstLine(text: string): string {
  const newlineIndex = text.indexOf('\n');
  return newlineIndex === -1 ? text : text.slice(0, newlineIndex + 1);
}

function concatUint8Arrays(chunks: Uint8Array[], totalLength: number): Uint8Array {
  const combined = new Uint8Array(totalLength);
  let offset = 0;
  for (const chunk of chunks) {
    combined.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return combined;
}

function parseZip64ExtraField(
  view: DataView,
  extraStart: number,
  extraLen: number,
  needUncompressed: boolean,
  needCompressed: boolean,
  needLocalHeaderOffset: boolean,
): { uncompressedSize?: number; compressedSize?: number; localHeaderOffset?: number } {
  let pos = extraStart;
  const extraEnd = extraStart + extraLen;
  while (pos + 4 <= extraEnd) {
    const headerId = view.getUint16(pos, true);
    const dataSize = view.getUint16(pos + 2, true);
    const dataStart = pos + 4;
    const dataEnd = dataStart + dataSize;
    if (dataEnd > extraEnd) {
      break;
    }
    if (headerId === ZIP64_EXTRA_HEADER_ID) {
      let fieldPos = dataStart;
      const result: {
        uncompressedSize?: number;
        compressedSize?: number;
        localHeaderOffset?: number;
      } = {};
      if (needUncompressed && fieldPos + 8 <= dataEnd) {
        result.uncompressedSize = toSafeNumber(view.getBigUint64(fieldPos, true));
        fieldPos += 8;
      }
      if (needCompressed && fieldPos + 8 <= dataEnd) {
        result.compressedSize = toSafeNumber(view.getBigUint64(fieldPos, true));
        fieldPos += 8;
      }
      if (needLocalHeaderOffset && fieldPos + 8 <= dataEnd) {
        result.localHeaderOffset = toSafeNumber(view.getBigUint64(fieldPos, true));
      }
      return result;
    }
    pos = dataEnd;
  }
  return {};
}

async function resolveCentralDirectoryRange(
  file: Blob,
): Promise<{ cdOffset: number; cdSize: number }> {
  if (file.size < EOCD_MIN_SIZE) {
    throw new Error('Invalid ZIP archive: file is too small.');
  }

  const tailSize = Math.min(file.size, MAX_EOCD_SEARCH_BYTES);
  const tailStart = file.size - tailSize;
  const tailBuf = await file.slice(tailStart, file.size).arrayBuffer();
  const tailView = new DataView(tailBuf);

  let eocdOffset = -1;
  for (let offset = tailBuf.byteLength - EOCD_MIN_SIZE; offset >= 0; offset--) {
    if (tailView.getUint32(offset, true) === EOCD_SIGNATURE) {
      const commentLen = tailView.getUint16(offset + 20, true);
      if (offset + EOCD_MIN_SIZE + commentLen === tailBuf.byteLength) {
        eocdOffset = offset;
        break;
      }
    }
  }

  if (eocdOffset === -1) {
    throw new Error('Invalid ZIP archive: End of Central Directory not found.');
  }

  const eocdFileOffset = tailStart + eocdOffset;
  let cdSize = tailView.getUint32(eocdOffset + 12, true);
  let cdOffset = tailView.getUint32(eocdOffset + 16, true);
  const hasZip64Sentinel = cdSize === 0xffffffff || cdOffset === 0xffffffff;

  if (eocdFileOffset >= ZIP64_LOCATOR_SIZE) {
    const locBuf =
      eocdOffset >= ZIP64_LOCATOR_SIZE
        ? tailBuf.slice(eocdOffset - ZIP64_LOCATOR_SIZE, eocdOffset)
        : await file.slice(eocdFileOffset - ZIP64_LOCATOR_SIZE, eocdFileOffset).arrayBuffer();
    const locView = new DataView(locBuf);
    if (locView.getUint32(0, true) === ZIP64_LOCATOR_SIGNATURE) {
      const zip64EocdOffset = toSafeNumber(locView.getBigUint64(8, true));
      if (zip64EocdOffset + ZIP64_EOCD_MIN_SIZE > eocdFileOffset - ZIP64_LOCATOR_SIZE) {
        throw new Error('Invalid ZIP64 archive: EOCD offset out of bounds.');
      }
      const z64Buf = await file
        .slice(zip64EocdOffset, zip64EocdOffset + ZIP64_EOCD_MIN_SIZE)
        .arrayBuffer();
      const z64View = new DataView(z64Buf);
      if (z64View.getUint32(0, true) !== ZIP64_EOCD_SIGNATURE) {
        throw new Error('Invalid ZIP64 archive: corrupt ZIP64 EOCD record.');
      }
      if (cdSize === 0xffffffff) {
        cdSize = toSafeNumber(z64View.getBigUint64(40, true));
      }
      if (cdOffset === 0xffffffff) {
        cdOffset = toSafeNumber(z64View.getBigUint64(48, true));
      }
    } else if (hasZip64Sentinel) {
      throw new Error('Invalid ZIP64 archive: missing ZIP64 EOCD Locator.');
    }
  } else if (hasZip64Sentinel) {
    throw new Error('Invalid ZIP64 archive: missing ZIP64 EOCD Locator.');
  }

  if (cdSize > MAX_CD_BYTES) {
    throw new Error('ZIP Central Directory exceeds maximum supported size.');
  }
  if (cdOffset < 0 || cdSize < 0 || cdOffset + cdSize > eocdFileOffset) {
    throw new Error('Invalid ZIP archive: Central Directory out of bounds.');
  }

  return { cdOffset, cdSize };
}

function findEntriesCsvInCentralDirectory(cdBuf: ArrayBuffer): ZipEntryLocation | null {
  const cdView = new DataView(cdBuf);
  const decoder = new TextDecoder('utf-8');
  let pos = 0;

  while (pos + CD_FIXED_HEADER_SIZE <= cdBuf.byteLength) {
    if (cdView.getUint32(pos, true) !== CD_SIGNATURE) {
      throw new Error('Invalid ZIP archive: corrupt Central Directory entry.');
    }

    const flags = cdView.getUint16(pos + 8, true);
    const compressionMethod = cdView.getUint16(pos + 10, true);
    const compressedSize32 = cdView.getUint32(pos + 20, true);
    const uncompressedSize32 = cdView.getUint32(pos + 24, true);
    const fileNameLen = cdView.getUint16(pos + 28, true);
    const extraLen = cdView.getUint16(pos + 30, true);
    const commentLen = cdView.getUint16(pos + 32, true);
    const localHeaderOffset32 = cdView.getUint32(pos + 42, true);

    const recordEnd = pos + CD_FIXED_HEADER_SIZE + fileNameLen + extraLen + commentLen;
    if (recordEnd > cdBuf.byteLength) {
      throw new Error('Invalid ZIP archive: truncated Central Directory entry.');
    }

    const fileName = decoder.decode(new Uint8Array(cdBuf, pos + CD_FIXED_HEADER_SIZE, fileNameLen));

    if (fileName === 'entries.csv') {
      const needUncompressed = uncompressedSize32 === 0xffffffff;
      const needCompressed = compressedSize32 === 0xffffffff;
      const needLocalHeaderOffset = localHeaderOffset32 === 0xffffffff;

      const z64Extra =
        needUncompressed || needCompressed || needLocalHeaderOffset
          ? parseZip64ExtraField(
              cdView,
              pos + CD_FIXED_HEADER_SIZE + fileNameLen,
              extraLen,
              needUncompressed,
              needCompressed,
              needLocalHeaderOffset,
            )
          : {};

      const compressedSize = z64Extra.compressedSize ?? compressedSize32;
      const uncompressedSize = z64Extra.uncompressedSize ?? uncompressedSize32;
      const localHeaderOffset = z64Extra.localHeaderOffset ?? localHeaderOffset32;

      if (
        (needUncompressed && z64Extra.uncompressedSize === undefined) ||
        (needCompressed && z64Extra.compressedSize === undefined) ||
        (needLocalHeaderOffset && z64Extra.localHeaderOffset === undefined)
      ) {
        throw new Error('Invalid ZIP64 archive: missing ZIP64 entry sizes.');
      }

      return {
        compressionMethod,
        flags,
        compressedSize,
        uncompressedSize,
        localHeaderOffset,
      };
    }

    pos = recordEnd;
  }

  return null;
}

async function fallbackExtractWithJSZip(file: Blob): Promise<string | null> {
  if (file.size > MAX_JSZIP_FALLBACK_BYTES) {
    throw new Error(
      'Archive is too large to inspect without browser streaming decompression support.',
    );
  }
  const JSZipModule = await import('jszip');
  const JSZip = JSZipModule.default;
  const loadedZip = await new JSZip().loadAsync(file);
  const csvFile = loadedZip.file('entries.csv');
  if (!csvFile) {
    return null;
  }
  const csvText = await csvFile.async('text');
  return sliceFirstLine(csvText);
}

/**
 * Extracts only the header line (first line, up to and including `\n`) of
 * `entries.csv` from a ZIP archive using `Blob.slice()` and streaming
 * `DecompressionStream('deflate-raw')` without loading the archive into memory.
 *
 * Returns `null` if `entries.csv` does not exist at the archive root.
 */
export async function extractEntriesCsvHeaderLine(
  file: Blob,
  maxHeaderBytes = 65536,
): Promise<string | null> {
  if (maxHeaderBytes <= 0) {
    return '';
  }

  const { cdOffset, cdSize } = await resolveCentralDirectoryRange(file);
  if (cdSize === 0) {
    return null;
  }

  const cdBuf = await file.slice(cdOffset, cdOffset + cdSize).arrayBuffer();
  const entry = findEntriesCsvInCentralDirectory(cdBuf);
  if (!entry) {
    return null;
  }

  if ((entry.flags & 0x0001) !== 0) {
    throw new Error('Encrypted ZIP archives are not supported.');
  }

  if (entry.localHeaderOffset + LFH_FIXED_HEADER_SIZE > file.size) {
    throw new Error('Invalid ZIP archive: Local File Header out of bounds.');
  }

  const lfhBuf = await file
    .slice(entry.localHeaderOffset, entry.localHeaderOffset + LFH_FIXED_HEADER_SIZE)
    .arrayBuffer();
  const lfhView = new DataView(lfhBuf);
  if (lfhView.getUint32(0, true) !== LFH_SIGNATURE) {
    throw new Error('Invalid ZIP archive: corrupt Local File Header.');
  }

  const lfhFileNameLen = lfhView.getUint16(26, true);
  const lfhExtraLen = lfhView.getUint16(28, true);
  const dataStart = entry.localHeaderOffset + LFH_FIXED_HEADER_SIZE + lfhFileNameLen + lfhExtraLen;

  if (dataStart + entry.compressedSize > file.size) {
    throw new Error('Invalid ZIP archive: entry data out of bounds.');
  }

  if (entry.compressionMethod === 0) {
    const bytesToRead = Math.min(entry.uncompressedSize, maxHeaderBytes);
    if (bytesToRead === 0) {
      return '';
    }
    const sliceBuf = await file.slice(dataStart, dataStart + bytesToRead).arrayBuffer();
    const text = new TextDecoder('utf-8').decode(sliceBuf);
    return sliceFirstLine(text);
  }

  if (entry.compressionMethod === 8) {
    if (entry.compressedSize === 0 && entry.uncompressedSize === 0) {
      return '';
    }

    if (typeof DecompressionStream === 'undefined') {
      return fallbackExtractWithJSZip(file);
    }

    const stream = file
      .slice(dataStart, dataStart + entry.compressedSize)
      .stream()
      .pipeThrough(new DecompressionStream('deflate-raw'));
    const reader = stream.getReader();

    const chunks: Uint8Array[] = [];
    let totalBytes = 0;

    try {
      while (totalBytes < maxHeaderBytes) {
        const { value, done } = await reader.read();
        if (done || !value) {
          break;
        }

        const nlIndex = value.indexOf(0x0a);
        if (nlIndex !== -1) {
          const take = Math.min(nlIndex + 1, maxHeaderBytes - totalBytes);
          chunks.push(value.subarray(0, take));
          totalBytes += take;
          await reader.cancel();
          break;
        }

        const remaining = maxHeaderBytes - totalBytes;
        if (value.byteLength >= remaining) {
          chunks.push(value.subarray(0, remaining));
          totalBytes += remaining;
          await reader.cancel();
          break;
        }

        chunks.push(value);
        totalBytes += value.byteLength;
      }
    } finally {
      reader.releaseLock();
    }

    const text = new TextDecoder('utf-8').decode(concatUint8Arrays(chunks, totalBytes));
    return sliceFirstLine(text);
  }

  throw new Error(`Unsupported ZIP compression method: ${entry.compressionMethod}`);
}
