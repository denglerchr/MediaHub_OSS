// frontend/src/app/pipes/format-bytes.pipe.ts
import { Pipe, PipeTransform } from '@angular/core';

@Pipe({
  name: 'formatBytes',
  standalone: true,
})
export class FormatBytesPipe implements PipeTransform {
  /**
   * Transforms a number of bytes into a human-readable string (e.g., "1.25 MB").
   * @param bytes The number of bytes.
   * @param decimals The number of decimal places to include.
   * @returns A formatted string.
   */
  transform(bytes: number | null | undefined, decimals = 2): string {
    if (bytes == null || bytes === 0) return '0 Bytes';

    const k = 1024;
    const dm = decimals < 0 ? 0 : decimals;
    const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB'];

    if (bytes <= 0) return '0 Bytes';

    const i = Math.floor(Math.log(bytes) / Math.log(k));
    const sizeIndex = Math.max(0, Math.min(i, sizes.length - 1));

    return parseFloat((bytes / Math.pow(k, sizeIndex)).toFixed(dm)) + ' ' + sizes[sizeIndex];
  }
}
