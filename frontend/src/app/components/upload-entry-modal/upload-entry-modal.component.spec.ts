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
  let selectedDatabase$: BehaviorSubject<Database>;
  let fetchCurrentUserSpy: jasmine.Spy;
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
    selectedDatabase$ = new BehaviorSubject(db);
    fetchCurrentUserSpy = jasmine.createSpy('fetchCurrentUser').and.returnValue(of(user));

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
        { provide: DatabaseService, useValue: { selectedDatabase$ } },
        { provide: AuthService, useValue: { fetchCurrentUser: fetchCurrentUserSpy } },
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

  it('nests custom fields under custom_fields FormGroup so a custom field named "file" does not collide with root file control', fakeAsync(() => {
    const dbWithCollidingField = {
      id: 'db-cf',
      name: 'DB Custom File',
      content_type: ContentType.Image,
      custom_fields: [
        { id: 1, name: 'file', type: 'TEXT' },
        { id: 2, name: 'verified', type: 'BOOLEAN' },
      ],
    } as unknown as Database;

    selectedDatabase$.next(dbWithCollidingField);
    modalService.open(UploadEntryModalComponent.MODAL_ID);
    fixture.detectChanges();

    const rootFileInput = fixture.nativeElement.querySelector('input#file') as HTMLInputElement;
    const customFileInput = fixture.nativeElement.querySelector(
      'input#upload-cf-file',
    ) as HTMLInputElement;
    expect(rootFileInput).toBeTruthy();
    expect(customFileInput).toBeTruthy();

    const uploadFile = new File(['img-bytes'], 'photo.jpg', { type: 'image/jpeg' });
    component.selectedFile = uploadFile;
    component.uploadForm.patchValue({
      file: uploadFile,
      timestamp: '2026-10-06T12:00:00',
      custom_fields: {
        file: 'original-source-name',
        verified: true,
      },
    });

    expect(component.uploadForm.get('file')?.value).toBe(uploadFile);
    expect(component.uploadForm.get(['custom_fields', 'file'])?.value).toBe('original-source-name');

    uploadEntrySpy.and.returnValue(of(undefined));
    component.onSubmit();
    tick();

    expect(uploadEntrySpy).toHaveBeenCalledTimes(1);
    const [, metadata, submittedFile] = uploadEntrySpy.calls.mostRecent().args;
    expect(submittedFile).toBe(uploadFile);
    expect(metadata.custom_fields).toEqual({
      file: 'original-source-name',
      verified: true,
    });
  }));

  it('vetoes modalService.close via close guard while uploading and snapshots batch files across re-open', fakeAsync(() => {
    const modalEvents: string[] = [];
    const sub = modalService
      .getModalEvents(UploadEntryModalComponent.MODAL_ID)
      .subscribe((e) => modalEvents.push(e.action));

    const userSubject = new Subject<User>();
    fetchCurrentUserSpy.and.returnValue(userSubject);

    modalService.open(UploadEntryModalComponent.MODAL_ID);
    fixture.detectChanges();

    const fileA = new File(['a'], 'a.png', { type: 'image/png' });
    const fileB = new File(['b'], 'b.png', { type: 'image/png' });
    const fileC = new File(['c'], 'c.png', { type: 'image/png' });
    component.handleMultipleFiles([fileA, fileB]);

    uploadEntrySpy.and.returnValue(of(undefined));
    component.onSubmit();
    expect(component.isLoading).toBeTrue();

    // Attempting to close while isLoading === true must be vetoed by the close guard.
    modalEvents.length = 0;
    modalService.close(UploadEntryModalComponent.MODAL_ID);
    expect(modalEvents).not.toContain('close');

    // Re-opening replaces selectedFiles/selectedFile on the component instance.
    modalService.open(UploadEntryModalComponent.MODAL_ID, { file: fileC });
    fixture.detectChanges();
    expect(component.isLoading).toBeFalse();
    expect(component.isMultipleUpload).toBeFalse();
    expect(component.selectedFile).toBe(fileC);
    expect(component.selectedFiles).toEqual([]);

    // Now the snapshotted in-flight upload resolves fetchCurrentUser and proceeds
    // with the original [fileA, fileB] snapshot without touching the re-opened session.
    userSubject.next(user);
    userSubject.complete();
    tick();

    expect(uploadEntrySpy).toHaveBeenCalledTimes(2);
    expect(uploadEntrySpy.calls.argsFor(0)[2]).toBe(fileA);
    expect(uploadEntrySpy.calls.argsFor(1)[2]).toBe(fileB);
    expect(component.selectedFile).toBe(fileC);
    expect(component.totalUploads).toBe(0);

    sub.unsubscribe();
  }));

  it('validates INTEGER custom fields and partial COORDINATE pairs and skips non-finite numeric coercion', fakeAsync(() => {
    const schemaDb = {
      id: 'db-schema',
      name: 'Schema DB',
      content_type: ContentType.Image,
      custom_fields: [
        { id: 1, name: 'count', type: 'INTEGER' },
        { id: 2, name: 'score', type: 'REAL' },
        { id: 3, name: 'coords', type: 'COORDINATE' },
      ],
    } as unknown as Database;

    selectedDatabase$.next(schemaDb);
    modalService.open(UploadEntryModalComponent.MODAL_ID);
    fixture.detectChanges();

    primeUploadForm();

    const countCtrl = component.uploadForm.get(['custom_fields', 'count']);
    const scoreCtrl = component.uploadForm.get(['custom_fields', 'score']);
    const coordsGroup = component.uploadForm.get(['custom_fields', 'coords']);

    countCtrl?.setValue(2.5);
    expect(countCtrl?.invalid).toBeTrue();
    expect(component.uploadForm.invalid).toBeTrue();

    countCtrl?.setValue(-3);
    expect(countCtrl?.valid).toBeTrue();

    coordsGroup?.patchValue({ latitude: 48.1, longitude: null });
    expect(coordsGroup?.hasError('incompleteCoordinate')).toBeTrue();
    expect(component.uploadForm.invalid).toBeTrue();

    coordsGroup?.patchValue({ latitude: 48.1, longitude: 11.5 });
    scoreCtrl?.setValue('Infinity');
    expect(component.uploadForm.valid).toBeTrue();

    uploadEntrySpy.and.returnValue(of(undefined));
    component.onSubmit();
    tick();

    expect(uploadEntrySpy).toHaveBeenCalledTimes(1);
    const [, metadata] = uploadEntrySpy.calls.mostRecent().args;
    expect(metadata.custom_fields).toEqual({
      count: -3,
      coords: { latitude: 48.1, longitude: 11.5 },
    });
  }));

  it('marks timestamp pristine on handleFile and ignores stale extractMetadata results after switching files', async () => {
    const notificationSpy = TestBed.inject(
      NotificationService,
    ) as jasmine.SpyObj<NotificationService>;

    // Build a minimal valid JPEG with EXIF DateTimeOriginal = "2020:01:02 03:04:05"
    const exifBytes = new Uint8Array(128);
    // JPEG SOI + APP1 marker + length
    exifBytes[0] = 0xff;
    exifBytes[1] = 0xd8;
    exifBytes[2] = 0xff;
    exifBytes[3] = 0xe1;
    exifBytes[4] = 0x00;
    exifBytes[5] = 0x70;
    // "Exif\0\0"
    exifBytes.set([0x45, 0x78, 0x69, 0x66, 0x00, 0x00], 6);
    // TIFF header at offset 12: "II" + 42 + IFD0 offset 8
    const tiff = 12;
    const view = new DataView(exifBytes.buffer);
    view.setUint16(tiff, 0x4949, false);
    view.setUint16(tiff + 2, 42, true);
    view.setUint32(tiff + 4, 8, true);
    // IFD0 at tiff + 8: 1 entry (ExifSubIFD 0x8769 -> offset 26)
    view.setUint16(tiff + 8, 1, true);
    view.setUint16(tiff + 10, 0x8769, true);
    view.setUint16(tiff + 12, 4, true);
    view.setUint32(tiff + 14, 1, true);
    view.setUint32(tiff + 18, 26, true);
    // ExifSubIFD at tiff + 26: 1 entry (DateTimeOriginal 0x9003, 20 bytes at offset 44)
    view.setUint16(tiff + 26, 1, true);
    view.setUint16(tiff + 28, 0x9003, true);
    view.setUint16(tiff + 30, 2, true);
    view.setUint32(tiff + 32, 20, true);
    view.setUint32(tiff + 36, 44, true);
    const dateStr = '2020:01:02 03:04:05\0';
    for (let i = 0; i < dateStr.length; i++) {
      exifBytes[tiff + 44 + i] = dateStr.charCodeAt(i);
    }

    let resolveSlice!: (buf: ArrayBuffer) => void;
    const fileA = new File([exifBytes], 'first.jpg', { type: 'image/jpeg' });
    spyOn(fileA, 'slice').and.returnValue({
      arrayBuffer: () =>
        new Promise<ArrayBuffer>((resolve) => {
          resolveSlice = resolve;
        }),
    } as unknown as Blob);

    const fileB = new File(['png'], 'second.png', { type: 'image/png' });

    component.uploadForm.get('timestamp')?.markAsDirty();
    const firstPromise = component.handleFile(fileA);
    expect(component.uploadForm.get('timestamp')?.pristine).toBeTrue();

    await component.handleFile(fileB);
    expect(component.selectedFile).toBe(fileB);

    // Resolve stale fileA extraction AFTER fileB is already active
    resolveSlice(exifBytes.buffer);
    await firstPromise;

    expect(component.selectedFile).toBe(fileB);
    expect(String(component.uploadForm.get('timestamp')?.value)).not.toContain('2020-01-02');
    expect(notificationSpy.showSuccess).not.toHaveBeenCalled();
  });
});
