import { SimpleChange } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { of, throwError } from 'rxjs';
import { ContentType, Database } from '../../../core/models';
import { DatabaseService } from '../../../core/services/database.service';
import { ModalService } from '../../../core/services/modal.service';
import { NotificationService } from '../../../core/services/notification.service';
import { DatabaseCustomFieldsEditorComponent } from './database-custom-fields-editor.component';

describe('DatabaseCustomFieldsEditorComponent', () => {
  let fixture: ComponentFixture<DatabaseCustomFieldsEditorComponent>;
  let component: DatabaseCustomFieldsEditorComponent;
  let databaseSpy: jasmine.SpyObj<DatabaseService>;
  let modalSpy: jasmine.SpyObj<ModalService>;
  let notificationSpy: jasmine.SpyObj<NotificationService>;

  const musicDb: Database = {
    id: 'db1',
    name: 'Music',
    content_type: ContentType.Audio,
    n_max_queued: 0,
    config: { create_preview: true, auto_conversion: '' },
    housekeeping: { interval: '0', disk_space: '0', max_age: '0' },
    custom_fields: [],
  };

  beforeEach(async () => {
    databaseSpy = jasmine.createSpyObj<DatabaseService>('DatabaseService', [
      'addCustomField',
      'updateCustomField',
      'deleteCustomField',
    ]);
    modalSpy = jasmine.createSpyObj<ModalService>('ModalService', ['confirm']);
    notificationSpy = jasmine.createSpyObj<NotificationService>('NotificationService', [
      'showSuccess',
      'showError',
    ]);

    await TestBed.configureTestingModule({
      imports: [DatabaseCustomFieldsEditorComponent],
      providers: [
        { provide: DatabaseService, useValue: databaseSpy },
        { provide: ModalService, useValue: modalSpy },
        { provide: NotificationService, useValue: notificationSpy },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(DatabaseCustomFieldsEditorComponent);
    component = fixture.componentInstance;
    component.database = musicDb;
    component.canAdmin = true;
    fixture.detectChanges();
  });

  it('keeps editingFieldId open when updateCustomField fails and closes it on success', () => {
    const field = { id: 7, name: 'artist', type: 'TEXT' as const, is_indexed: true };
    component.database = { ...musicDb, custom_fields: [field] };
    fixture.detectChanges();

    component.onStartEditField(field);
    component.editingFieldName = 'composer';
    databaseSpy.updateCustomField.and.returnValue(throwError(() => new Error('update failed')));

    component.onSaveField(7);
    expect(component.editingFieldId).toBe(7);
    expect(component.isMutatingField).toBeFalse();

    databaseSpy.updateCustomField.and.returnValue(
      of({ id: 7, name: 'composer', type: 'TEXT', is_indexed: true }),
    );
    component.onSaveField(7);
    expect(component.editingFieldId).toBeNull();
    expect(component.isMutatingField).toBeFalse();
  });

  it('resets isMutatingField without clearing newFieldName when addCustomField fails, and clears on success', () => {
    databaseSpy.addCustomField.and.returnValue(throwError(() => new Error('add failed')));

    component.newFieldName = 'album';
    component.onAddField();
    expect(component.isMutatingField).toBeFalse();
    expect(component.newFieldName).toBe('album');

    databaseSpy.addCustomField.and.returnValue(
      of({ id: 1, name: 'album', type: 'TEXT', is_indexed: true }),
    );
    component.onAddField();
    expect(component.isMutatingField).toBeFalse();
    expect(component.newFieldName).toBe('');
    expect(component.newFieldType).toBe('TEXT');
    expect(component.newFieldIsIndexed).toBeTrue();
  });

  it('rejects reserved and duplicate custom field names via shared validation', () => {
    component.newFieldName = 'filename';
    component.onAddField();
    expect(notificationSpy.showError).toHaveBeenCalledWith(
      "Field name 'filename' is reserved (standard/media field). Please choose another name.",
    );

    component.database = {
      ...musicDb,
      custom_fields: [{ id: 1, name: 'Genre', type: 'TEXT', is_indexed: true }],
    };
    fixture.detectChanges();

    notificationSpy.showError.calls.reset();
    component.newFieldName = 'genre';
    component.onAddField();
    expect(notificationSpy.showError).toHaveBeenCalledWith(
      "A custom field named 'genre' already exists.",
    );
  });

  it('confirms deletion before calling deleteCustomField', () => {
    const field = { id: 3, name: 'mood', type: 'TEXT' as const, is_indexed: false };
    component.database = { ...musicDb, custom_fields: [field] };

    modalSpy.confirm.and.returnValue(of(false));
    component.onDeleteField(field);
    expect(modalSpy.confirm).toHaveBeenCalledTimes(1);
    expect(databaseSpy.deleteCustomField).not.toHaveBeenCalled();

    modalSpy.confirm.and.returnValue(of(true));
    databaseSpy.deleteCustomField.and.returnValue(of({ message: 'deleted' }));
    component.onDeleteField(field);
    expect(databaseSpy.deleteCustomField).toHaveBeenCalledWith('db1', 3);
    expect(component.isMutatingField).toBeFalse();
  });

  it('resets editingFieldId when switching to a different database', () => {
    component.editingFieldId = 7;

    component.ngOnChanges({
      database: new SimpleChange(musicDb, { ...musicDb, id: 'db1' }, false),
    });
    expect(component.editingFieldId).toBe(7);

    component.ngOnChanges({
      database: new SimpleChange(musicDb, { ...musicDb, id: 'db2' }, false),
    });
    expect(component.editingFieldId).toBeNull();
  });

  it('resets add-field draft state when switching to a different database', () => {
    component.newFieldName = 'draft_field';
    component.newFieldType = 'INTEGER';
    component.newFieldIsIndexed = true;

    const db2: Database = { ...musicDb, id: 'db2', name: 'Photos' };
    component.database = db2;
    component.ngOnChanges({
      database: new SimpleChange(musicDb, db2, false),
    });

    expect(component.newFieldName).toBe('');
    expect(component.newFieldType).toBe('TEXT');
    expect(component.newFieldIsIndexed).toBeFalse();
  });

  it('exits edit mode when canAdmin becomes false', () => {
    component.editingFieldId = 7;
    component.canAdmin = false;
    component.ngOnChanges({
      canAdmin: new SimpleChange(true, false, false),
    });

    expect(component.editingFieldId).toBeNull();
  });
});
