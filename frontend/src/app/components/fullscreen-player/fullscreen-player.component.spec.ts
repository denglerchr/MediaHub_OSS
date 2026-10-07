// frontend/src/app/components/fullscreen-player/fullscreen-player.component.spec.ts
// Regression specs for FE-011/N-D1 (token expiry mid-presentation): the media URL
// regeneration after a media element error must force an access-token refresh
// first — the failing Range request carried the *expired* token and its 401 never
// reaches the interceptor, so nothing else has refreshed it.
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { CommonModule } from '@angular/common';
import { of } from 'rxjs';
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
          useValue: { getEntryFileUrl: getEntryFileUrlSpy, searchEntries: () => of([]) },
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
});
