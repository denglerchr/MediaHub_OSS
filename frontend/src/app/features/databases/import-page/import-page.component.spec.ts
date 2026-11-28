// frontend/src/app/pages/import-page/import-page.component.spec.ts
// Regression spec for FE-050/N-D7: the CSV columns hidden from the mapping step
// must follow the *database's content type* (mirror of Go's
// media.GetMetadataFields(db.ContentType), entryhandler/utils_import.go:282).
// Hiding width/height/duration/channels for every database made e.g. a `width`
// column invisible in the mapping step on an audio DB while the backend still
// counted it as an unmapped custom column — aborting the import under
// unmapped_fields:'fail'.
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { NO_ERRORS_SCHEMA } from '@angular/core';
import { ActivatedRoute, convertToParamMap, ParamMap, Router, RouterLink } from '@angular/router';
import { HttpEvent, HttpEventType, HttpResponse } from '@angular/common/http';
import { BehaviorSubject, Subject, of, throwError } from 'rxjs';
import JSZip from 'jszip';
import { ImportPageComponent } from './import-page.component';
import { PageHeaderComponent } from '../../../shared/components/page-header/page-header.component';
import { FileDragDropDirective } from '../../../shared/directives/file-drag-drop.directive';
import { DatabaseService } from '../../../core/services/database.service';
import { EntryService } from '../../../core/services/entry.service';
import { NotificationService } from '../../../core/services/notification.service';
import { ContentType, Database } from '../../../core/models';

describe('ImportPageComponent — FE-050/N-D7 content-type-aware hidden columns', () => {
  let fixture: ComponentFixture<ImportPageComponent>;
  let component: ImportPageComponent;
  let paramMapSubject: BehaviorSubject<ParamMap>;
  let selectDatabaseSpy: jasmine.Spy;
  let importEntriesSpy: jasmine.Spy;
  let notificationSpy: jasmine.SpyObj<NotificationService>;

  const makeDb = (contentType: ContentType): Database =>
    ({
      id: 'db1',
      name: 'DB One',
      content_type: contentType,
      custom_fields: [{ id: 1, name: 'tag', type: 'TEXT' }],
    }) as unknown as Database;

  const csvWith = (extraHeaders: string[]): string =>
    [
      'id',
      'filename',
      'timestamp',
      'filesize',
      'previewsize',
      'mime_type',
      'status',
      ...extraHeaders,
    ].join(',') + '\n';

  const parseHeaders = (csv: string): Promise<void> => {
    // Test cast (declared FE-042 exemption): the parsing step is private and
    // driven by the ZIP reader in production.
    const internals = component as unknown as {
      extractCsvHeaders(csvText: string): Promise<void>;
    };
    return internals.extractCsvHeaders(csv);
  };

  beforeEach(async () => {
    paramMapSubject = new BehaviorSubject<ParamMap>(convertToParamMap({ id: 'db1' }));
    selectDatabaseSpy = jasmine
      .createSpy('selectDatabase')
      .and.returnValue(of(makeDb(ContentType.Image)));
    importEntriesSpy = jasmine.createSpy('importEntries');
    notificationSpy = jasmine.createSpyObj('NotificationService', [
      'showError',
      'showSuccess',
      'showInfo',
    ]);

    await TestBed.configureTestingModule({
      imports: [ImportPageComponent],
      providers: [
        { provide: ActivatedRoute, useValue: { paramMap: paramMapSubject.asObservable() } },
        {
          provide: DatabaseService,
          useValue: { selectDatabase: selectDatabaseSpy },
        },
        { provide: EntryService, useValue: { importEntries: importEntriesSpy } },
        {
          provide: NotificationService,
          useValue: notificationSpy,
        },
        // Router is only needed for DI — the specs never navigate.
        { provide: Router, useValue: {} },
      ],
    })
      .overrideComponent(ImportPageComponent, {
        remove: { imports: [PageHeaderComponent, FileDragDropDirective, RouterLink] },
        add: { schemas: [NO_ERRORS_SCHEMA] },
      })
      .compileComponents();

    fixture = TestBed.createComponent(ImportPageComponent);
    component = fixture.componentInstance;
  });

  it('FE-050/N-D7: on an audio DB a `width` column stays mappable (only duration/channels are media fields)', async () => {
    component.currentDatabase = makeDb(ContentType.Audio);

    await parseHeaders(csvWith(['width', 'channels', 'note']));

    expect(component.customCsvHeaders).toEqual(['width', 'note']);
  });

  it('FE-050/N-D7: on an image DB width/height are hidden but `channels` stays mappable', async () => {
    component.currentDatabase = makeDb(ContentType.Image);

    await parseHeaders(csvWith(['width', 'height', 'channels', 'note']));

    expect(component.customCsvHeaders).toEqual(['channels', 'note']);
  });

  it('FE-050: standard field columns are always hidden (created_at & co. stay mappable)', async () => {
    component.currentDatabase = makeDb(ContentType.File);

    await parseHeaders(csvWith(['created_at', 'updated_at', 'preview_filesize', 'note']));

    expect(component.customCsvHeaders).toEqual([
      'created_at',
      'updated_at',
      'preview_filesize',
      'note',
    ]);
  });

  it('extracts CSV headers from a real ZIP archive via extractEntriesCsvHeaderLine', async () => {
    component.currentDatabase = makeDb(ContentType.Image);

    const zip = new JSZip();
    zip.file(
      'entries.csv',
      csvWith(['width', 'height', 'custom_tag']) +
        '1,img.jpg,1700000000000,100,20,image/jpeg,ready,800,600,nature\n',
    );
    zip.file('media/img.jpg', new Uint8Array([1, 2, 3, 4]));
    const blob = await zip.generateAsync({ type: 'blob', compression: 'DEFLATE' });
    const zipFile = new File([blob], 'archive.zip', { type: 'application/zip' });

    await component.onFileSelected(zipFile);

    expect(component.selectedFile).toBe(zipFile);
    expect(component.isParsingZip).toBeFalse();
    expect(component.customCsvHeaders).toEqual(['custom_tag']);
    expect(notificationSpy.showError).not.toHaveBeenCalled();
  });

  it('shows an error and resets when a real ZIP archive lacks entries.csv', async () => {
    component.currentDatabase = makeDb(ContentType.Image);

    const zip = new JSZip();
    zip.file('media/img.jpg', new Uint8Array([1, 2, 3]));
    const blob = await zip.generateAsync({ type: 'blob', compression: 'STORE' });
    const zipFile = new File([blob], 'missing-csv.zip', { type: 'application/zip' });

    await component.onFileSelected(zipFile);

    expect(component.selectedFile).toBeNull();
    expect(component.isParsingZip).toBeFalse();
    expect(notificationSpy.showError).toHaveBeenCalledWith(
      'The archive does not contain an entries.csv file in the root folder.',
    );
  });

  it('handles selectDatabase errors in ngOnInit and renders an error panel', () => {
    selectDatabaseSpy.and.returnValue(throwError(() => new Error('DB not found')));

    expect(() => component.ngOnInit()).not.toThrow();
    expect(component.currentDatabase).toBeNull();
    expect(component.dbLoadError).toBeTrue();

    fixture.detectChanges();
    expect(fixture.nativeElement.textContent).toContain('Failed to Load Database');
  });

  it('strips UTF-8 BOM from entries.csv headers before parsing', async () => {
    component.currentDatabase = makeDb(ContentType.Image);

    await parseHeaders('\uFEFF' + csvWith(['note']));

    expect(component.customCsvHeaders).toEqual(['note']);
    expect(notificationSpy.showError).not.toHaveBeenCalled();
  });

  it('rejects entries.csv with invalid or out-of-order standard headers', async () => {
    component.currentDatabase = makeDb(ContentType.Image);
    component.selectedFile = new File(['x'], 'bad.zip', { type: 'application/zip' });

    await parseHeaders('filename,id,timestamp,filesize,previewsize,mime_type,status\n');

    expect(notificationSpy.showError).toHaveBeenCalled();
    expect(component.selectedFile).toBeNull();
  });

  it('rejects entries.csv with duplicate headers (exact or case-insensitive)', async () => {
    component.currentDatabase = makeDb(ContentType.Image);
    component.selectedFile = new File(['x'], 'dup1.zip', { type: 'application/zip' });

    await parseHeaders(csvWith(['note', 'note']));
    expect(notificationSpy.showError).toHaveBeenCalled();
    expect(component.selectedFile).toBeNull();

    notificationSpy.showError.calls.reset();
    component.selectedFile = new File(['x'], 'dup2.zip', { type: 'application/zip' });
    await parseHeaders(csvWith(['note', 'NOTE']));
    expect(notificationSpy.showError).toHaveBeenCalled();
    expect(component.selectedFile).toBeNull();
  });

  it('blocks startImport when two CSV columns map to the same custom field', async () => {
    component.currentDatabase = makeDb(ContentType.Image);
    component.selectedFile = new File(['x'], 'ok.zip', { type: 'application/zip' });

    await parseHeaders(csvWith(['col_a', 'col_b']));
    component.mappingStepForm.patchValue({ col_a: 'tag', col_b: 'tag' });

    component.startImport();

    expect(notificationSpy.showError).toHaveBeenCalled();
    expect(importEntriesSpy).not.toHaveBeenCalled();
    expect(component.isUploading).toBeFalse();
  });

  it('cancels an in-flight import upload and resets upload state', () => {
    component.currentDatabase = makeDb(ContentType.Image);
    component.selectedFile = new File(['x'], 'ok.zip', { type: 'application/zip' });
    component.currentStep = 4;

    const upload$ = new Subject<HttpEvent<unknown>>();
    importEntriesSpy.and.returnValue(upload$.asObservable());

    component.startImport();
    upload$.next({ type: HttpEventType.UploadProgress, loaded: 50, total: 100 });
    expect(component.isUploading).toBeTrue();
    expect(component.uploadProgress).toBe(50);

    fixture.detectChanges();
    const cancelBtn = Array.from(
      fixture.nativeElement.querySelectorAll('button') as NodeListOf<HTMLButtonElement>,
    ).find((btn) => btn.textContent?.includes('Cancel Upload'));
    expect(cancelBtn).toBeTruthy();

    cancelBtn!.click();

    expect(component.isUploading).toBeFalse();
    expect(component.uploadProgress).toBe(0);
    expect(notificationSpy.showInfo).toHaveBeenCalledWith('Import upload cancelled.');

    // Further events on the cancelled stream must not complete the upload
    upload$.next(new HttpResponse({ status: 200 }));
    expect(component.isUploadComplete).toBeFalse();
  });

  it('resets stepper/file state and cancels in-flight upload when route id changes', () => {
    const db2 = { ...makeDb(ContentType.Audio), id: 'db2', name: 'DB Two' };
    selectDatabaseSpy.and.callFake((id: string) =>
      of(id === 'db2' ? db2 : makeDb(ContentType.Image)),
    );

    fixture.detectChanges();
    expect(component.currentDatabase?.id).toBe('db1');

    const upload$ = new Subject<HttpEvent<unknown>>();
    importEntriesSpy.and.returnValue(upload$.asObservable());

    component.selectedFile = new File(['x'], 'ok.zip', { type: 'application/zip' });
    component.customCsvHeaders = ['tag'];
    component.currentStep = 4;
    component.startImport();
    expect(component.isUploading).toBeTrue();

    paramMapSubject.next(convertToParamMap({ id: 'db2' }));
    fixture.detectChanges();

    expect(selectDatabaseSpy).toHaveBeenCalledWith('db2');
    expect(component.currentDatabase?.id).toBe('db2');
    expect(component.isUploading).toBeFalse();
    expect(component.selectedFile).toBeNull();
    expect(component.customCsvHeaders).toEqual([]);
    expect(component.currentStep).toBe(1);

    // Cancelled upload stream from db1 must not complete on db2
    upload$.next(new HttpResponse({ status: 200 }));
    expect(component.isUploadComplete).toBeFalse();
  });
});
