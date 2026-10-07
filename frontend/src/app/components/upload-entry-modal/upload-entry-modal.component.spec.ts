// frontend/src/app/components/upload-entry-modal/upload-entry-modal.component.spec.ts
// Regression spec for N3 (Phase D): a background upload completing must never
// close a modal instance the user re-opened after dismissing the original one.
import { ComponentFixture, TestBed, fakeAsync, tick } from '@angular/core/testing';
import { NO_ERRORS_SCHEMA } from '@angular/core';
import { CommonModule } from '@angular/common';
import { ReactiveFormsModule } from '@angular/forms';
import { FormatBytesPipe } from '../../pipes/format-bytes.pipe';
import { BehaviorSubject, Subject, of, throwError } from 'rxjs';
import { UploadEntryModalComponent } from './upload-entry-modal.component';
import { EntryService } from '../../services/entry.service';
import { DatabaseService } from '../../services/database.service';
import { AuthService } from '../../services/auth.service';
import { ModalService } from '../../services/modal.service';
import { NotificationService } from '../../services/notification.service';
import { Database, User } from '../../models';
import { ContentType } from '../../models/enums';

describe('UploadEntryModalComponent — N3 background-upload auto-close', () => {
  let fixture: ComponentFixture<UploadEntryModalComponent>;
  let component: UploadEntryModalComponent;
  let modalService: ModalService;
  let upload$: Subject<void>;
  let uploadEntrySpy: jasmine.Spy;

  const db = {
    id: 'db1',
    name: 'DB One',
    content_type: ContentType.Image,
    custom_fields: [],
  } as unknown as Database;

  const user = { id: 'u1', username: 'alice', is_admin: true, permissions: [] } as unknown as User;

  beforeEach(async () => {
    upload$ = new Subject<void>();
    uploadEntrySpy = jasmine.createSpy('uploadEntry').and.returnValue(upload$);

    await TestBed.configureTestingModule({
      declarations: [UploadEntryModalComponent],
      imports: [CommonModule, ReactiveFormsModule, FormatBytesPipe],
      schemas: [NO_ERRORS_SCHEMA], // <app-modal> wrapper is not under test
      providers: [
        {
          provide: EntryService,
          useValue: {
            uploadEntry: uploadEntrySpy,
            triggerImageListRefresh: jasmine.createSpy('triggerImageListRefresh'),
          },
        },
        { provide: DatabaseService, useValue: { selectedDatabase$: new BehaviorSubject(db) } },
        { provide: AuthService, useValue: { fetchCurrentUser: () => of(user) } },
        {
          provide: NotificationService,
          useValue: jasmine.createSpyObj('NotificationService', [
            'showError',
            'showSuccess',
            'showInfo',
            'showGlobalError',
            'clearGlobalError',
          ]),
        },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(UploadEntryModalComponent);
    component = fixture.componentInstance;
    modalService = TestBed.inject(ModalService);
    fixture.detectChanges(); // runs ngOnInit (form initialisation via selectedDatabase$)
  });

  function primeUploadForm(): void {
    component.uploadForm.patchValue({ timestamp: '2026-10-06T12:00:00' });
    component.selectedFile = new File(['x'], 'a.jpg', { type: 'image/jpeg' });
    component.uploadForm.patchValue({ file: component.selectedFile });
    expect(component.uploadForm.valid).toBeTrue();
  }

  it('N3: an upload completing in the background must NOT close a re-opened modal', fakeAsync(() => {
    const closeSpy = spyOn(modalService, 'close').and.callThrough();

    // session 1: open the modal and start the upload
    modalService.open(UploadEntryModalComponent.MODAL_ID);
    fixture.detectChanges();
    primeUploadForm();
    component.onSubmit();
    expect(component.isLoading).toBeTrue();

    // the user dismisses the modal mid-upload (ESC/X/overlay path) and re-opens it
    modalService.close(UploadEntryModalComponent.MODAL_ID);
    modalService.open(UploadEntryModalComponent.MODAL_ID);
    fixture.detectChanges();
    closeSpy.calls.reset();

    // the background upload finishes
    upload$.next();
    upload$.complete();
    tick();

    // the re-opened modal belongs to a new session — it must stay open
    expect(closeSpy).not.toHaveBeenCalled();
    expect(component.isLoading).toBeFalse(); // and must not stay locked by the old upload
  }));

  it('N3 guard: an upload completing while its own session is open still auto-closes', fakeAsync(() => {
    const closeSpy = spyOn(modalService, 'close').and.callThrough();

    modalService.open(UploadEntryModalComponent.MODAL_ID);
    fixture.detectChanges();
    primeUploadForm();
    component.onSubmit();

    upload$.next();
    upload$.complete();
    tick();

    expect(closeSpy).toHaveBeenCalledWith(UploadEntryModalComponent.MODAL_ID);
    expect(component.isLoading).toBeFalse();
  }));

  it('N3: a failing background upload reports the error but leaves the re-opened modal alone', fakeAsync(() => {
    const closeSpy = spyOn(modalService, 'close').and.callThrough();
    const notificationSpy = TestBed.inject(
      NotificationService,
    ) as jasmine.SpyObj<NotificationService>;

    modalService.open(UploadEntryModalComponent.MODAL_ID);
    fixture.detectChanges();
    primeUploadForm();
    component.onSubmit();

    modalService.close(UploadEntryModalComponent.MODAL_ID);
    modalService.open(UploadEntryModalComponent.MODAL_ID);
    fixture.detectChanges();
    closeSpy.calls.reset();

    upload$.error({ message: 'boom' });
    tick();

    expect(closeSpy).not.toHaveBeenCalled();
    expect(notificationSpy.showError).toHaveBeenCalled();
    expect(component.isLoading).toBeFalse();
  }));

  // --- FE-022: batch upload continues past failures -------------------------

  it('FE-022: a batch continues past a failing file and reports exactly one summary toast', fakeAsync(() => {
    const modalCloseSpy = spyOn(modalService, 'close').and.callThrough();
    const notificationSpy = TestBed.inject(
      NotificationService,
    ) as jasmine.SpyObj<NotificationService>;
    const refreshSpy = TestBed.inject(EntryService).triggerImageListRefresh as jasmine.Spy;

    modalService.open(UploadEntryModalComponent.MODAL_ID);
    fixture.detectChanges();

    const okFile1 = new File(['x'], 'ok1.png', { type: 'image/png' });
    const badFile = new File(['x'], 'bad.png', { type: 'image/png' });
    const okFile2 = new File(['x'], 'ok2.png', { type: 'image/png' });
    component.handleMultipleFiles([okFile1, badFile, okFile2]);
    expect(component.selectedFiles.length).toBe(3);

    uploadEntrySpy.and.callFake((_dbId: string, _metadata: unknown, file: File) =>
      file.name === 'bad.png' ? throwError(() => new Error('boom')) : of(undefined),
    );

    component.onSubmit();
    tick();

    expect(uploadEntrySpy)
      .withContext('the queue must continue after the failing file')
      .toHaveBeenCalledTimes(3);
    expect(notificationSpy.showError)
      .withContext('one summary error toast for the whole batch')
      .toHaveBeenCalledTimes(1);
    const summary = notificationSpy.showError.calls.mostRecent().args[0];
    expect(summary).toContain('Uploaded 2 of 3');
    expect(summary).toContain('bad.png');
    expect(notificationSpy.showSuccess).not.toHaveBeenCalled();
    expect(refreshSpy).toHaveBeenCalled();
    expect(modalCloseSpy)
      .withContext('a partially successful batch finishes and closes')
      .toHaveBeenCalledWith(UploadEntryModalComponent.MODAL_ID);
  }));

  it('FE-022: after a total batch failure the modal stays open so the queue can be retried', fakeAsync(() => {
    const modalCloseSpy = spyOn(modalService, 'close').and.callThrough();
    const notificationSpy = TestBed.inject(
      NotificationService,
    ) as jasmine.SpyObj<NotificationService>;

    modalService.open(UploadEntryModalComponent.MODAL_ID);
    fixture.detectChanges();

    component.handleMultipleFiles([
      new File(['x'], 'a.png', { type: 'image/png' }),
      new File(['x'], 'b.png', { type: 'image/png' }),
    ]);
    uploadEntrySpy.and.returnValue(throwError(() => new Error('server down')));

    component.onSubmit();
    tick();

    expect(uploadEntrySpy).toHaveBeenCalledTimes(2);
    expect(notificationSpy.showError).toHaveBeenCalledTimes(1);
    expect(modalCloseSpy).not.toHaveBeenCalled();
    expect(component.isLoading).toBeFalse();
  }));

  it('FE-022: handleMultipleFiles validates MIME types (single source of truth for multi-drops)', () => {
    const notificationSpy = TestBed.inject(
      NotificationService,
    ) as jasmine.SpyObj<NotificationService>;
    const good1 = new File(['x'], 'a.jpg', { type: 'image/jpeg' });
    const good2 = new File(['x'], 'b.png', { type: 'image/png' });
    const bad = new File(['x'], 'notes.txt', { type: 'text/plain' });

    component.handleMultipleFiles([good1, bad, good2]);

    expect(component.selectedFiles.map((f) => f.name))
      .withContext('invalid types must not reach the backend (it answers 415)')
      .toEqual(['a.jpg', 'b.png']);
    expect(notificationSpy.showInfo).toHaveBeenCalledWith('Skipped 1 file(s) of invalid type.');

    component.handleMultipleFiles([bad]);
    expect(component.selectedFiles).toEqual([]);
    expect(notificationSpy.showError)
      .withContext('an all-invalid drop must say so')
      .toHaveBeenCalled();
  });
});
