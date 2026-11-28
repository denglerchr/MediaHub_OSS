// frontend/src/app/utils/media-playback.controller.spec.ts
import { Subject, Subscription, of } from 'rxjs';
import { MediaPlaybackController } from './media-playback.controller';
import { EntryService } from '../../core/services/entry.service';
import { AuthService } from '../../core/services/auth.service';
import { NotificationService } from '../../core/services/notification.service';

describe('MediaPlaybackController', () => {
  let controller: MediaPlaybackController;
  let getEntryFileUrlSpy: jasmine.Spy;
  let getEntryFileBlobSpy: jasmine.Spy;
  let forceAccessTokenRefreshSpy: jasmine.Spy;
  let notificationService: jasmine.SpyObj<NotificationService>;

  beforeEach(() => {
    getEntryFileUrlSpy = jasmine
      .createSpy('getEntryFileUrl')
      .and.returnValue('/api/database/db1/entry/7/file?token=t1');
    getEntryFileBlobSpy = jasmine
      .createSpy('getEntryFileBlob')
      .and.returnValue(of(new Blob(['img'], { type: 'image/jpeg' })));
    forceAccessTokenRefreshSpy = jasmine
      .createSpy('forceAccessTokenRefresh')
      .and.returnValue(of(undefined));
    notificationService = jasmine.createSpyObj('NotificationService', ['showError']);

    controller = new MediaPlaybackController(
      {
        getEntryFileUrl: getEntryFileUrlSpy,
        getEntryFileBlob: getEntryFileBlobSpy,
      } as unknown as EntryService,
      { forceAccessTokenRefresh: forceAccessTokenRefreshSpy } as unknown as AuthService,
      notificationService,
    );
  });

  afterEach(() => {
    controller.destroy();
  });

  it('delegates createStreamUrl to EntryService.getEntryFileUrl', () => {
    const url = controller.createStreamUrl('db1', 7);
    expect(getEntryFileUrlSpy).toHaveBeenCalledWith('db1', 7);
    expect(url).toBe('/api/database/db1/entry/7/file?token=t1');
  });

  it('loads image blobs, creates and tracks object URLs, and revokes previous URLs', () => {
    let n = 0;
    const createSpy = spyOn(URL, 'createObjectURL').and.callFake(() => `blob:obj-${++n}`);
    const revokeSpy = spyOn(URL, 'revokeObjectURL');

    let firstUrl = '';
    controller.loadImageBlob('db1', 1).subscribe((u) => (firstUrl = u));
    expect(firstUrl).toBe('blob:obj-1');
    expect(controller.isCurrentObjectUrl('blob:obj-1')).toBeTrue();
    expect(controller.isCurrentObjectUrl('blob:other')).toBeFalse();
    expect(controller.isCurrentObjectUrl(null)).toBeFalse();

    let secondUrl = '';
    controller.loadImageBlob('db1', 2).subscribe((u) => (secondUrl = u));
    expect(createSpy).toHaveBeenCalledTimes(2);
    expect(revokeSpy).toHaveBeenCalledWith('blob:obj-1');
    expect(secondUrl).toBe('blob:obj-2');
    expect(controller.isCurrentObjectUrl('blob:obj-2')).toBeTrue();

    controller.revokeObjectUrl();
    expect(revokeSpy).toHaveBeenCalledWith('blob:obj-2');
    expect(controller.isCurrentObjectUrl('blob:obj-2')).toBeFalse();
  });

  it('tracks and cancels in-flight blob subscriptions and resets state for a new entry', () => {
    const revokeSpy = spyOn(URL, 'revokeObjectURL');
    spyOn(URL, 'createObjectURL').and.returnValue('blob:active');
    controller.loadImageBlob('db1', 1).subscribe();

    const sub = new Subscription();
    spyOn(sub, 'unsubscribe').and.callThrough();
    controller.trackBlobSubscription(sub);

    controller.resetForNewEntry();

    expect(sub.unsubscribe).toHaveBeenCalledTimes(1);
    expect(revokeSpy).toHaveBeenCalledWith('blob:active');
    expect(controller.isCurrentObjectUrl('blob:active')).toBeFalse();
  });

  it('detaches single and iterable HTMLMediaElements safely', () => {
    const makeEl = () =>
      ({
        pause: jasmine.createSpy('pause'),
        removeAttribute: jasmine.createSpy('removeAttribute'),
        load: jasmine.createSpy('load'),
      }) as unknown as HTMLMediaElement;

    const single = makeEl();
    controller.detachMediaElements(single);
    expect(single.pause).toHaveBeenCalledTimes(1);
    expect(single.removeAttribute).toHaveBeenCalledWith('src');
    expect(single.load).toHaveBeenCalledTimes(1);

    const a = makeEl();
    const b = makeEl();
    controller.detachMediaElements([a, b]);
    expect(a.pause).toHaveBeenCalledTimes(1);
    expect(b.pause).toHaveBeenCalledTimes(1);
    expect(a.removeAttribute).toHaveBeenCalledWith('src');
    expect(b.load).toHaveBeenCalledTimes(1);

    expect(() => controller.detachMediaElements(null)).not.toThrow();
    expect(() => controller.detachMediaElements(undefined)).not.toThrow();
  });

  it('refreshes token on stream media error, retries up to 2 times, and restores playback position', () => {
    let n = 1;
    getEntryFileUrlSpy.and.callFake(() => `/api/database/db1/entry/7/file?token=t${n}`);
    forceAccessTokenRefreshSpy.and.callFake(() => {
      n++;
      return of(undefined);
    });

    const mediaEl = { currentTime: 42.5 } as HTMLMediaElement;
    const onRetryUrl = jasmine.createSpy('onRetryUrl');
    const onFatalError = jasmine.createSpy('onFatalError');

    let currentUrl: string | null = '/api/database/db1/entry/7/file?token=t1';
    onRetryUrl.and.callFake((freshUrl: string) => {
      currentUrl = freshUrl;
    });

    // Retry 1
    controller.handleMediaError({
      dbId: 'db1',
      entryId: 7,
      currentUrl,
      mediaElement: mediaEl,
      isStillActive: () => true,
      onRetryUrl,
      onFatalError,
    });

    expect(onRetryUrl).toHaveBeenCalledWith('/api/database/db1/entry/7/file?token=t2');
    expect(onFatalError).not.toHaveBeenCalled();
    expect(notificationService.showError).not.toHaveBeenCalled();

    // Restore playback position
    mediaEl.currentTime = 0;
    controller.restorePlaybackPosition(mediaEl);
    expect(mediaEl.currentTime).toBe(42.5);

    // Subsequent restore without pending position is a no-op
    mediaEl.currentTime = 10;
    controller.restorePlaybackPosition(mediaEl);
    expect(mediaEl.currentTime).toBe(10);

    // Retry 2
    controller.handleMediaError({
      dbId: 'db1',
      entryId: 7,
      currentUrl,
      mediaElement: mediaEl,
      isStillActive: () => true,
      onRetryUrl,
      onFatalError,
    });
    expect(onRetryUrl).toHaveBeenCalledWith('/api/database/db1/entry/7/file?token=t3');
    expect(notificationService.showError).not.toHaveBeenCalled();

    // Retry 3 exceeds MEDIA_URL_MAX_RETRIES (2) -> fatal error
    controller.handleMediaError({
      dbId: 'db1',
      entryId: 7,
      currentUrl,
      mediaElement: mediaEl,
      isStillActive: () => true,
      onRetryUrl,
      onFatalError,
    });
    expect(notificationService.showError).toHaveBeenCalledTimes(1);
    expect(onFatalError).toHaveBeenCalledTimes(1);
  });

  it('reports fatal error without retrying when refreshed URL is unchanged', () => {
    const onRetryUrl = jasmine.createSpy('onRetryUrl');
    const onFatalError = jasmine.createSpy('onFatalError');

    controller.handleMediaError({
      dbId: 'db1',
      entryId: 7,
      currentUrl: '/api/database/db1/entry/7/file?token=t1',
      isStillActive: () => true,
      onRetryUrl,
      onFatalError,
    });

    expect(forceAccessTokenRefreshSpy).toHaveBeenCalledTimes(1);
    expect(onRetryUrl).not.toHaveBeenCalled();
    expect(notificationService.showError).toHaveBeenCalledTimes(1);
    expect(onFatalError).toHaveBeenCalledTimes(1);
  });

  it('reports fatal error immediately without token refresh for current blob object URLs', () => {
    spyOn(URL, 'createObjectURL').and.returnValue('blob:local-1');
    controller.loadImageBlob('db1', 7).subscribe();

    const onRetryUrl = jasmine.createSpy('onRetryUrl');
    const onFatalError = jasmine.createSpy('onFatalError');

    controller.handleMediaError({
      dbId: 'db1',
      entryId: 7,
      currentUrl: 'blob:local-1',
      isStillActive: () => true,
      onRetryUrl,
      onFatalError,
    });

    expect(forceAccessTokenRefreshSpy).not.toHaveBeenCalled();
    expect(onRetryUrl).not.toHaveBeenCalled();
    expect(notificationService.showError).toHaveBeenCalledTimes(1);
    expect(onFatalError).toHaveBeenCalledTimes(1);
  });

  it('ignores refresh completion when isStillActive returns false or after destroy', () => {
    const refresh$ = new Subject<void>();
    forceAccessTokenRefreshSpy.and.returnValue(refresh$.asObservable());
    getEntryFileUrlSpy.and.returnValue('/api/database/db1/entry/7/file?token=fresh');

    const onRetryUrl = jasmine.createSpy('onRetryUrl');
    let active = true;

    controller.handleMediaError({
      dbId: 'db1',
      entryId: 7,
      currentUrl: '/api/database/db1/entry/7/file?token=old',
      isStillActive: () => active,
      onRetryUrl,
    });

    active = false;
    refresh$.next();
    expect(onRetryUrl).not.toHaveBeenCalled();
    expect(notificationService.showError).not.toHaveBeenCalled();

    // Also ignores null currentUrl
    controller.handleMediaError({
      dbId: 'db1',
      entryId: 7,
      currentUrl: null,
      isStillActive: () => true,
      onRetryUrl,
    });
    expect(forceAccessTokenRefreshSpy).toHaveBeenCalledTimes(1);

    // Cancels in-flight refresh on destroy
    active = true;
    const refresh2$ = new Subject<void>();
    forceAccessTokenRefreshSpy.and.returnValue(refresh2$.asObservable());
    controller.handleMediaError({
      dbId: 'db1',
      entryId: 7,
      currentUrl: '/api/database/db1/entry/7/file?token=old',
      isStillActive: () => true,
      onRetryUrl,
    });
    controller.destroy();
    refresh2$.next();
    expect(onRetryUrl).not.toHaveBeenCalled();
  });

  it('swallows seek errors when restoring playback position', () => {
    let n = 1;
    getEntryFileUrlSpy.and.callFake(() => `/api/database/db1/entry/7/file?token=t${n}`);
    forceAccessTokenRefreshSpy.and.callFake(() => {
      n++;
      return of(undefined);
    });

    let throwOnSet = false;
    const mediaEl = {
      get currentTime() {
        return 12;
      },
      set currentTime(_v: number) {
        if (throwOnSet) {
          throw new Error('seek refused');
        }
      },
    } as unknown as HTMLMediaElement;

    controller.handleMediaError({
      dbId: 'db1',
      entryId: 7,
      currentUrl: '/api/database/db1/entry/7/file?token=t1',
      mediaElement: mediaEl,
      isStillActive: () => true,
      onRetryUrl: () => {},
    });

    throwOnSet = true;
    expect(() => controller.restorePlaybackPosition(mediaEl)).not.toThrow();
  });

  it('deduplicates concurrent handleMediaError calls while token refresh is in flight without burning retries', () => {
    const refresh$ = new Subject<void>();
    let tokenGen = 1;
    getEntryFileUrlSpy.and.callFake(() => `/api/database/db1/entry/7/file?token=t${tokenGen}`);
    forceAccessTokenRefreshSpy.and.callFake(() => refresh$.asObservable());

    const onRetryUrl = jasmine.createSpy('onRetryUrl');
    const onFatalError = jasmine.createSpy('onFatalError');

    controller.handleMediaError({
      dbId: 'db1',
      entryId: 7,
      currentUrl: '/api/database/db1/entry/7/file?token=t1',
      isStillActive: () => true,
      onRetryUrl,
      onFatalError,
    });
    controller.handleMediaError({
      dbId: 'db1',
      entryId: 7,
      currentUrl: '/api/database/db1/entry/7/file?token=t1',
      isStillActive: () => true,
      onRetryUrl,
      onFatalError,
    });

    expect(forceAccessTokenRefreshSpy).toHaveBeenCalledTimes(1);

    tokenGen = 2;
    refresh$.next();
    refresh$.complete();

    expect(onRetryUrl).toHaveBeenCalledTimes(1);
    expect(onRetryUrl).toHaveBeenCalledWith('/api/database/db1/entry/7/file?token=t2');
    expect(onFatalError).not.toHaveBeenCalled();

    // Only 1 retry was consumed, so a second failure with the new URL still retries (retry 2 of 2).
    forceAccessTokenRefreshSpy.and.callFake(() => {
      tokenGen = 3;
      return of(undefined);
    });
    controller.handleMediaError({
      dbId: 'db1',
      entryId: 7,
      currentUrl: '/api/database/db1/entry/7/file?token=t2',
      isStillActive: () => true,
      onRetryUrl,
      onFatalError,
    });

    expect(onRetryUrl).toHaveBeenCalledTimes(2);
    expect(onRetryUrl).toHaveBeenCalledWith('/api/database/db1/entry/7/file?token=t3');
    expect(onFatalError).not.toHaveBeenCalled();
  });

  it('cancels pending error retry on resetForNewEntry and teardown even when isStillActive is true', () => {
    getEntryFileUrlSpy.and.returnValue('/api/database/db1/entry/7/file?token=fresh');
    const onRetryUrl = jasmine.createSpy('onRetryUrl');
    const onFatalError = jasmine.createSpy('onFatalError');

    // Case 1: resetForNewEntry() while refresh is pending
    const refresh1$ = new Subject<void>();
    forceAccessTokenRefreshSpy.and.returnValue(refresh1$.asObservable());
    controller.handleMediaError({
      dbId: 'db1',
      entryId: 7,
      currentUrl: '/api/database/db1/entry/7/file?token=old',
      isStillActive: () => true,
      onRetryUrl,
      onFatalError,
    });
    controller.resetForNewEntry();
    refresh1$.next();
    refresh1$.complete();

    expect(onRetryUrl).not.toHaveBeenCalled();
    expect(onFatalError).not.toHaveBeenCalled();

    // Case 2: teardown() while refresh is pending
    const refresh2$ = new Subject<void>();
    forceAccessTokenRefreshSpy.and.returnValue(refresh2$.asObservable());
    controller.handleMediaError({
      dbId: 'db1',
      entryId: 7,
      currentUrl: '/api/database/db1/entry/7/file?token=old',
      isStillActive: () => true,
      onRetryUrl,
      onFatalError,
    });
    controller.teardown();
    refresh2$.next();
    refresh2$.complete();

    expect(onRetryUrl).not.toHaveBeenCalled();
    expect(onFatalError).not.toHaveBeenCalled();
  });
});
