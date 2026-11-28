import { ComponentFixture, TestBed } from '@angular/core/testing';
import { of, Subject } from 'rxjs';
import { CreateDatabaseModalComponent } from './create-database-modal.component';
import { DatabaseService } from '../../../core/services/database.service';
import { ModalService } from '../../../core/services/modal.service';
import { Database } from '../../../core/models';

describe('CreateDatabaseModalComponent', () => {
  let fixture: ComponentFixture<CreateDatabaseModalComponent>;
  let component: CreateDatabaseModalComponent;
  let databaseSpy: jasmine.SpyObj<DatabaseService>;

  beforeEach(async () => {
    databaseSpy = jasmine.createSpyObj<DatabaseService>('DatabaseService', ['createDatabase']);
    databaseSpy.createDatabase.and.returnValue(of({} as Database));

    await TestBed.configureTestingModule({
      imports: [CreateDatabaseModalComponent],
      providers: [{ provide: DatabaseService, useValue: databaseSpy }, ModalService],
    }).compileComponents();

    fixture = TestBed.createComponent(CreateDatabaseModalComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  function fillValidBaseForm(): void {
    component.createDbForm.patchValue({
      name: 'vacation_photos',
      content_type: 'image',
      n_max_queued: 0,
    });
  }

  it('H5: detects duplicate custom field names and restores form validity when renaming the duplicate row', () => {
    fillValidBaseForm();
    component.addCustomField();
    component.addCustomField();

    const row0Name = component.customFields.at(0).get('name')!;
    const row1Name = component.customFields.at(1).get('name')!;

    row0Name.setValue('genre');
    row1Name.setValue('  GENRE ');
    fixture.detectChanges();

    expect(row0Name.hasError('duplicate')).toBeTrue();
    expect(row1Name.hasError('duplicate')).toBeTrue();
    expect(component.customFields.valid).toBeFalse();
    expect(component.createDbForm.valid).toBeFalse();

    // Renaming the duplicate row clears duplicate errors on both rows and recalculates parent validity
    row1Name.setValue('artist');
    fixture.detectChanges();

    expect(row0Name.hasError('duplicate')).toBeFalse();
    expect(row1Name.hasError('duplicate')).toBeFalse();
    expect(row0Name.valid).toBeTrue();
    expect(row1Name.valid).toBeTrue();
    expect(component.customFields.valid).toBeTrue();
    expect(component.createDbForm.valid).toBeTrue();
  });

  it('H5: restores form validity when removing the duplicate custom field row', () => {
    fillValidBaseForm();
    component.addCustomField();
    component.addCustomField();

    const row0Name = component.customFields.at(0).get('name')!;
    const row1Name = component.customFields.at(1).get('name')!;

    row0Name.setValue('genre');
    row1Name.setValue('genre');
    fixture.detectChanges();

    expect(row0Name.hasError('duplicate')).toBeTrue();
    expect(row1Name.hasError('duplicate')).toBeTrue();
    expect(component.createDbForm.valid).toBeFalse();

    // Removing the duplicate row recalculates the remaining row and parent form validity
    component.removeCustomField(1);
    fixture.detectChanges();

    expect(component.customFields.length).toBe(1);
    expect(row0Name.hasError('duplicate')).toBeFalse();
    expect(row0Name.valid).toBeTrue();
    expect(component.customFields.valid).toBeTrue();
    expect(component.createDbForm.valid).toBeTrue();
  });

  it('blocks modal dismissal via close guard while createDatabase is in flight and unregisters on destroy', () => {
    const modalService = TestBed.inject(ModalService);
    const create$ = new Subject<Database>();
    databaseSpy.createDatabase.and.returnValue(create$.asObservable());

    let modalResult: boolean | null | undefined;
    modalService
      .open<boolean>(CreateDatabaseModalComponent.MODAL_ID)
      .subscribe((res) => (modalResult = res));

    fillValidBaseForm();
    component.onSubmit();
    fixture.detectChanges();

    expect(component.isLoading).toBeTrue();
    const cancelBtn = (
      Array.from(fixture.nativeElement.querySelectorAll('button')) as HTMLButtonElement[]
    ).find((btn) => btn.textContent?.trim() === 'Cancel');
    expect(cancelBtn?.disabled).toBeTrue();

    // Close attempts via service (ESC / X / overlay / Cancel) must be vetoed while loading
    modalService.close(CreateDatabaseModalComponent.MODAL_ID, false);
    expect(modalResult).toBeUndefined();

    // Completing the request unlocks and closes the modal
    create$.next({} as Database);
    create$.complete();
    expect(component.isLoading).toBeFalse();
    expect(modalResult).toBeTrue();

    // Destroy unregisters the close guard
    component.isLoading = true;
    fixture.destroy();
    let afterDestroyResult: boolean | null | undefined;
    modalService
      .open<boolean>(CreateDatabaseModalComponent.MODAL_ID)
      .subscribe((res) => (afterDestroyResult = res));
    modalService.close(CreateDatabaseModalComponent.MODAL_ID, false);
    expect(afterDestroyResult).toBeFalse();
  });
});
