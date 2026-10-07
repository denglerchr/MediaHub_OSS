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
import { CommonModule } from '@angular/common';
import { ReactiveFormsModule } from '@angular/forms';
import { ActivatedRoute, Router } from '@angular/router';
import { of } from 'rxjs';
import { ImportPageComponent } from './import-page.component';
import { DatabaseService } from '../../services/database.service';
import { EntryService } from '../../services/entry.service';
import { NotificationService } from '../../services/notification.service';
import { AuthService } from '../../services/auth.service';
import { FormatBytesPipe } from '../../pipes/format-bytes.pipe';
import { ContentType, Database } from '../../models';

describe('ImportPageComponent — FE-050/N-D7 content-type-aware hidden columns', () => {
  let fixture: ComponentFixture<ImportPageComponent>;
  let component: ImportPageComponent;

  const makeDb = (contentType: ContentType): Database =>
    ({
      id: 'db1',
      name: 'DB One',
      content_type: contentType,
      custom_fields: [],
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
    await TestBed.configureTestingModule({
      declarations: [ImportPageComponent],
      imports: [CommonModule, ReactiveFormsModule, FormatBytesPipe],
      schemas: [NO_ERRORS_SCHEMA], // appFileDragDrop etc. are not under test
      providers: [
        { provide: ActivatedRoute, useValue: { snapshot: { paramMap: new Map([['id', 'db1']]) } } },
        {
          provide: DatabaseService,
          useValue: { selectDatabase: () => of(makeDb(ContentType.Image)) },
        },
        { provide: EntryService, useValue: {} },
        { provide: AuthService, useValue: {} },
        {
          provide: NotificationService,
          useValue: jasmine.createSpyObj('NotificationService', [
            'showError',
            'showSuccess',
            'showInfo',
          ]),
        },
        // Router is only needed for DI — the specs never navigate.
        { provide: Router, useValue: {} },
      ],
    }).compileComponents();

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
});
