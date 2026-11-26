// frontend/src/app/components/fullscreen-player/fullscreen-player.component.spec.ts
// Regression specs for FE-011/N-D1 (token expiry mid-presentation): the media URL
// regeneration after a media element error must force an access-token refresh
// first — the failing Range request carried the *expired* token and its 401 never
// reaches the interceptor, so nothing else has refreshed it.
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { CommonModule } from '@angular/common';
import { of, throwError } from 'rxjs';
import { FullscreenPlayerComponent } from './fullscreen-player.component';
import { FullscreenSettings } from '../fullscreen-settings-modal/fullscreen-settings-modal.component';
import { EntryService } from '../../services/entry.service';
import { AuthService } from '../../services/auth.service';
import { NotificationService } from '../../services/notification.service';
import { Entry } from '../../models';

describe('FullscreenPlayerComponent — FE-011/N-D1 media URL regeneration', () => {
  let fixture: ComponentFixture<FullscreenPlayerComponent>;
  let component: FullscreenPlayerComponent;
  let getEntryFileUrlSpy: jasmine.Spy;
  let getEntryFileBlobSpy: jasmine.Spy;
  let forceAccessTokenRefreshSpy: jasmine.Spy;
  let notificationService: jasmine.SpyObj<NotificationService>;

  const settings: FullscreenSettings = {
    delaySeconds: 3,
    entryLimit: 10,
    shuffle: false,
    repeat: false,
  };

  const makeEntry = (id: number): Entry =>
    ({
      id,
      timestamp: 1000,
      status: 'ready',
      mime_type: 'audio/mpeg',
      filename: 'song.mp3',
    }) as Entry;

  beforeEach(async () => {
    getEntryFileUrlSpy = jasmine
      .createSpy('getEntryFileUrl')
      .and.returnValue('/api/database/db1/entry/7/file');
    getEntryFileBlobSpy = jasmine
      .createSpy('getEntryFileBlob')
      .and.returnValue(of(new Blob(['img'], { type: 'image/jpeg' })));
    forceAccessTokenRefreshSpy = jasmine
      .createSpy('forceAccessTokenRefresh')
      .and.returnValue(of(undefined));
    notificationService = jasmine.createSpyObj('NotificationService', [
      'showError',
      'showSuccess',
      'showInfo',
    ]);

    await TestBed.configureTestingModule({
      declarations: [FullscreenPlayerComponent],
      imports: [CommonModule],
      providers: [
        {
          provide: EntryService,
          useValue: {
            getEntryFileUrl: getEntryFileUrlSpy,
            getEntryFileBlob: getEntryFileBlobSpy,
            searchEntries: () => of([]),
          },
        },
        { provide: AuthService, useValue: { forceAccessTokenRefresh: forceAccessTokenRefreshSpy } },
        { provide: NotificationService, useValue: notificationService },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(FullscreenPlayerComponent);
    component = fixture.componentInstance;
    // The real Fullscreen API rejects without a user gesture and would surface an
    // unrelated toast — keep it out of these specs (the 5 s poll is harmless).
    const internals = component as unknown as { requestNativeFullscreen(): void };
    spyOn(internals, 'requestNativeFullscreen');
    component.settings = settings;
    component.dbId = 'db1';
    component.contentType = 'audio';
    component.currentEntry = makeEntry(7);
    component.mediaUrl = '/api/database/db1/entry/7/file';
  });

  it('FE-011/N-D1: a media error forces a token refresh and retries with the renewed URL', () => {
    let token = 'expired-token';
    getEntryFileUrlSpy.and.callFake(() => `/api/database/db1/entry/7/file?token=${token}`);
    forceAccessTokenRefreshSpy.and.callFake(() => {
      token = 'fresh-token';
      return of(undefined);
    });
    // The URL the failing media element was loaded with (snapshotted token).
    component.mediaUrl = getEntryFileUrlSpy();

    component.onMediaError();

    expect(forceAccessTokenRefreshSpy).toHaveBeenCalled();
    expect(getEntryFileUrlSpy).toHaveBeenCalledTimes(2);
    expect(component.mediaUrl).toContain('token=fresh-token');
    expect(notificationService.showError).not.toHaveBeenCalled();
  });

  it('FE-011: the failure is reported (and not retried) when the refresh yields no new URL', () => {
    component.onMediaError();

    expect(forceAccessTokenRefreshSpy).toHaveBeenCalled();
    expect(notificationService.showError).toHaveBeenCalledTimes(1);
    expect(component.mediaUrl).toBe('/api/database/db1/entry/7/file');
  });

  it('FE-011: the toast appears only after the two real retries are spent', () => {
    let n = 0;
    getEntryFileUrlSpy.and.callFake(() => `/api/database/db1/entry/7/file?token=t${++n}`);
    // The URL the failing media element was loaded with (first generation).
    component.mediaUrl = getEntryFileUrlSpy();

    component.onMediaError();
    component.onMediaError();
    expect(notificationService.showError).not.toHaveBeenCalled();

    component.onMediaError();
    expect(notificationService.showError).toHaveBeenCalledTimes(1);
  });

  it('FE-011: a refresh that completes after the playlist advanced leaves the new entry alone', () => {
    getEntryFileUrlSpy.and.callFake(
      (_dbId: string, entryId: number) => `/api/database/db1/entry/${entryId}/file?token=fresh`,
    );
    forceAccessTokenRefreshSpy.and.callFake(() => {
      // The 5 s poll switched to another entry while the refresh was in flight.
      component.currentEntry = makeEntry(8);
      component.mediaUrl = '/api/database/db1/entry/8/file?token=fresh';
      return of(undefined);
    });

    component.onMediaError();

    expect(component.mediaUrl).toBe('/api/database/db1/entry/8/file?token=fresh');
    expect(notificationService.showError).not.toHaveBeenCalled();
  });

  it('clears the playlist and stops playback when a poll returns an empty entries array', () => {
    const internals = component as unknown as {
      playlist: Entry[];
      currentIndex: number;
      knownEntryIds: Set<number>;
      handleFetchedEntries(entries: Entry[]): void;
    };

    internals.handleFetchedEntries([makeEntry(7)]);
    expect(internals.playlist.length).toBe(1);
    expect(component.currentEntry?.id).toBe(7);

    internals.handleFetchedEntries([]);

    expect(internals.playlist).toEqual([]);
    expect(internals.knownEntryIds.size).toBe(0);
    expect(component.currentEntry).toBeNull();
    expect(component.mediaUrl).toBeNull();
    expect(internals.currentIndex).toBe(-1);
    expect(component.isLoading).toBeFalse();
  });

  it('closes the player when Escape is pressed even without native fullscreen', () => {
    fixture.detectChanges();
    const exitSpy = spyOn(component.exit, 'emit');

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));

    expect(exitSpy).toHaveBeenCalledTimes(1);
    expect(component.mediaUrl).toBeNull();
  });

  it('fetches non-streamable images via getEntryFileBlob and revokes blob URLs on release', () => {
    const createSpy = spyOn(URL, 'createObjectURL').and.returnValue('blob:mock-1');
    const revokeSpy = spyOn(URL, 'revokeObjectURL');
    const startTimerSpy = spyOn(
      component as unknown as { startDelayTimer(): void },
      'startDelayTimer',
    );

    component.contentType = 'image';
    const imageEntry = {
      id: 11,
      timestamp: 1000,
      created_at: 1000,
      updated_at: 1000,
      status: 'ready',
      mime_type: 'image/jpeg',
      filename: 'photo.jpg',
    } as Entry;
    component.currentEntry = imageEntry;

    (component as unknown as { loadMedia(e: Entry): void }).loadMedia(imageEntry);

    expect(getEntryFileBlobSpy).toHaveBeenCalledWith('db1', 11);
    expect(createSpy).toHaveBeenCalled();
    expect(component.mediaUrl).toBe('blob:mock-1');
    expect(startTimerSpy).toHaveBeenCalledTimes(1);

    component.closePlayer();
    expect(revokeSpy).toHaveBeenCalledWith('blob:mock-1');
  });

  it('advances via startDelayTimer when image blob fetch fails', () => {
    getEntryFileBlobSpy.and.returnValue(throwError(() => new Error('401')));
    const startTimerSpy = spyOn(
      component as unknown as { startDelayTimer(): void },
      'startDelayTimer',
    );

    component.contentType = 'image';
    const imageEntry = {
      id: 12,
      timestamp: 1000,
      created_at: 1000,
      updated_at: 1000,
      status: 'ready',
      mime_type: 'image/jpeg',
      filename: 'photo.jpg',
    } as Entry;
    component.currentEntry = imageEntry;

    (component as unknown as { loadMedia(e: Entry): void }).loadMedia(imageEntry);

    expect(component.mediaUrl).toBeNull();
    expect(startTimerSpy).toHaveBeenCalledTimes(1);
  });

  it('advances via startDelayTimer on fatal onMediaError and restores currentTime on retry', () => {
    const startTimerSpy = spyOn(
      component as unknown as { startDelayTimer(): void },
      'startDelayTimer',
    );

    // Fatal error (same URL after refresh) starts delay timer
    component.onMediaError();
    expect(notificationService.showError).toHaveBeenCalledTimes(1);
    expect(startTimerSpy).toHaveBeenCalledTimes(1);

    // Retry captures currentTime and restores it in onMediaLoadedMetadata
    let token = 't1';
    getEntryFileUrlSpy.and.callFake(() => `/api/database/db1/entry/7/file?token=${token}`);
    component.isLoading = false;
    component.mediaUrl = getEntryFileUrlSpy();
    fixture.detectChanges();

    const audioEl = component.mediaElement!.nativeElement;
    let currentTimeVal = 42.5;
    Object.defineProperty(audioEl, 'currentTime', {
      configurable: true,
      get: () => currentTimeVal,
      set: (v: number) => {
        currentTimeVal = v;
      },
    });

    forceAccessTokenRefreshSpy.and.callFake(() => {
      token = 't2';
      return of(undefined);
    });
    component.onMediaError();

    currentTimeVal = 0;
    audioEl.dispatchEvent(new Event('loadedmetadata'));
    expect(currentTimeVal).toBe(42.5);
  });

  it('replays single-item repeat audio/video without waiting for src reload', () => {
    component.settings = { ...settings, repeat: true };
    const internals = component as unknown as {
      playlist: Entry[];
      currentIndex: number;
      playNext(): void;
    };
    const audioEntry = makeEntry(7);
    internals.playlist = [audioEntry];
    internals.currentIndex = 0;
    component.currentEntry = audioEntry;
    component.mediaUrl = '/api/database/db1/entry/7/file';

    const playSpy = jasmine.createSpy('play').and.returnValue(Promise.resolve());
    const fakeMedia = {
      currentTime: 18,
      play: playSpy,
      pause: () => {},
      removeAttribute: () => {},
      load: () => {},
    };
    component.mediaElement = { nativeElement: fakeMedia as unknown as HTMLMediaElement };

    internals.playNext();

    expect(fakeMedia.currentTime).toBe(0);
    expect(playSpy).toHaveBeenCalledTimes(1);
  });

  it('replaces playlist with newest entry in entryLimit=1 without repeat', () => {
    component.settings = { ...settings, entryLimit: 1, repeat: false };
    const internals = component as unknown as {
      playlist: Entry[];
      handleFetchedEntries(entries: Entry[]): void;
    };

    internals.handleFetchedEntries([makeEntry(1)]);
    expect(component.currentEntry?.id).toBe(1);

    internals.handleFetchedEntries([makeEntry(2)]);
    expect(internals.playlist.map((e) => e.id)).toEqual([2]);
    expect(component.currentEntry?.id).toBe(2);
  });

  it('renders empty state message when not loading and currentEntry is null', () => {
    component.isLoading = false;
    component.currentEntry = null;
    fixture.detectChanges();

    expect(fixture.nativeElement.textContent).toContain('No ready media entries available.');
  });
});
