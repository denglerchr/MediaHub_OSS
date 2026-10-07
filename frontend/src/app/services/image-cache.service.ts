// frontend/src/app/services/image-cache.service.ts
import { Injectable } from '@angular/core';
import { HttpClient, HttpHeaders } from '@angular/common/http';
import { Observable, throwError } from 'rxjs';
import { map, shareReplay, catchError, tap } from 'rxjs/operators';

@Injectable({
  providedIn: 'root',
})
export class ImageCacheService {
  private readonly MAX_CACHE_SIZE = 1024;
  private cache = new Map<string, Observable<string>>();
  private resolvedBlobUrls = new Map<string, string>();

  // FE-025: reference counts per source URL. Consumers (secure-image directive)
  // retain() a URL while an element displays (or is loading) its blob and release()
  // it when they stop using it.
  private refCounts = new Map<string, number>();

  // FE-025: source URLs whose cache entry was evicted while still referenced. Their
  // blob URL is revoked only once the last reference goes away — never while a
  // rendered <img> still points at it.
  private evictedWhileInUse = new Set<string>();

  // N5: blob URLs superseded by a re-fetch of the same source URL while the old one
  // was still referenced (evict-while-referenced → re-fetch). They are revoked
  // together with the current blob once the last reference goes away — exactly once
  // each, never while an <img> may still show them.
  private supersededBlobUrls = new Map<string, string[]>();

  constructor(private http: HttpClient) {}

  /**
   * Fetches an image blob from the given URL securely and returns a cached Object URL.
   * Multiple calls for the same URL share the exact same Observable and Blob URL.
   * Implements LRU eviction when cache reaches MAX_CACHE_SIZE (1024 entries).
   */
  public getBlobUrl(url: string): Observable<string> {
    if (this.cache.has(url)) {
      // Re-insert to refresh access order for LRU eviction
      const existing$ = this.cache.get(url)!;
      this.cache.delete(url);
      this.cache.set(url, existing$);
      return existing$;
    }

    this.evictOldestIfNecessary();

    const stream$ = this.http
      .get(url, {
        responseType: 'blob',
        headers: new HttpHeaders({ Accept: '*/*' }),
      })
      .pipe(
        map((blob) => URL.createObjectURL(blob)),
        tap((blobUrl) => {
          const previousBlobUrl = this.resolvedBlobUrls.get(url);
          if (previousBlobUrl && previousBlobUrl !== blobUrl) {
            // N5: this re-fetch superseded the previous blob for the same source URL.
            // The old blob may still be on screen (evict-while-referenced → re-fetch),
            // so remember it and revoke it with the current one instead of orphaning
            // it by overwriting the mapping.
            const superseded = this.supersededBlobUrls.get(url);
            if (superseded) {
              superseded.push(previousBlobUrl);
            } else {
              this.supersededBlobUrls.set(url, [previousBlobUrl]);
            }
          }
          this.resolvedBlobUrls.set(url, blobUrl);
          // FE-025: the entry can be evicted while the fetch is still in flight. If it
          // was and nothing references it any more, revoke as soon as it materialises.
          if (this.evictedWhileInUse.has(url) && (this.refCounts.get(url) ?? 0) === 0) {
            this.evictedWhileInUse.delete(url);
            this.revokeNow(url);
          }
        }),
        shareReplay(1),
        catchError((err) => {
          // N5: a failed fetch created no blob of its own — keep any previously
          // resolved (and possibly still referenced) mapping so it is still revoked
          // on release/invalidate instead of being orphaned here.
          this.cache.delete(url);
          return throwError(() => err);
        }),
      );

    this.cache.set(url, stream$);
    return stream$;
  }

  private evictOldestIfNecessary(): void {
    if (this.cache.size >= this.MAX_CACHE_SIZE) {
      const oldestKey = this.cache.keys().next().value;
      if (oldestKey) {
        this.invalidate(oldestKey);
      }
    }
  }

  /**
   * FE-025: marks the blob URL for `url` as in use (an element is displaying it or
   * about to). Eviction will defer revocation while the reference is held.
   */
  public retain(url: string): void {
    this.refCounts.set(url, (this.refCounts.get(url) ?? 0) + 1);
  }

  /**
   * FE-025: drops one usage of the blob URL for `url`. If the entry was evicted while
   * referenced, its blob URL is revoked now that nobody uses it any more.
   */
  public release(url: string): void {
    const remaining = (this.refCounts.get(url) ?? 0) - 1;
    if (remaining > 0) {
      this.refCounts.set(url, remaining);
      return;
    }
    this.refCounts.delete(url);
    if (this.evictedWhileInUse.has(url)) {
      this.evictedWhileInUse.delete(url);
      this.revokeNow(url);
    }
  }

  /**
   * Invalidates and revokes a single cached image URL without triggering un-subscribed HTTP requests.
   * Revocation is deferred while the URL is still referenced (FE-025).
   */
  public invalidate(url: string): void {
    this.cache.delete(url);
    if ((this.refCounts.get(url) ?? 0) > 0) {
      this.evictedWhileInUse.add(url);
      return;
    }
    this.revokeNow(url);
  }

  /**
   * Clears and revokes all materialized Blob URLs in memory without triggering un-subscribed HTTP requests.
   * URLs still referenced keep their blob until released (FE-025).
   */
  public clearAll(): void {
    const urls = Array.from(this.resolvedBlobUrls.keys());
    urls.forEach((url) => {
      this.cache.delete(url);
      if ((this.refCounts.get(url) ?? 0) > 0) {
        this.evictedWhileInUse.add(url);
      } else {
        this.revokeNow(url);
      }
    });
    this.cache.clear();
  }

  private revokeNow(url: string): void {
    // N5: whatever references this source URL is gone — drop the cache entry too,
    // so a later getBlobUrl() re-fetches instead of replaying a revoked blob URL.
    this.cache.delete(url);

    const blobUrl = this.resolvedBlobUrls.get(url);
    if (blobUrl) {
      this.resolvedBlobUrls.delete(url);
      this.revokeBlobUrl(blobUrl);
    }

    // N5: revoke blobs superseded by a re-fetch of this source URL as well.
    const superseded = this.supersededBlobUrls.get(url);
    if (superseded) {
      this.supersededBlobUrls.delete(url);
      superseded.forEach((oldBlobUrl) => this.revokeBlobUrl(oldBlobUrl));
    }
  }

  private revokeBlobUrl(blobUrl: string): void {
    try {
      URL.revokeObjectURL(blobUrl);
    } catch (_) {
      // Revoking an already-released blob URL is harmless — never let it break
      // the eviction flow.
    }
  }
}
