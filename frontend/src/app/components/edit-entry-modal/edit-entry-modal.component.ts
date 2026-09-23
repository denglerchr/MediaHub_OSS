import { Component, OnDestroy, OnInit } from '@angular/core';
import { FormBuilder, FormGroup, Validators } from '@angular/forms';
import { Subject } from 'rxjs';
import { takeUntil, filter, finalize } from 'rxjs/operators';
import { Database, Entry } from '../../models';
import { EntryService } from '../../services/entry.service';
import { DatabaseService } from '../../services/database.service';
import { ModalService } from '../../services/modal.service';

@Component({
  selector: 'app-edit-entry-modal',
  templateUrl: './edit-entry-modal.component.html',
  styleUrls: ['./edit-entry-modal.component.css'],
  standalone: false,
})
export class EditEntryModalComponent implements OnInit, OnDestroy {
  public static readonly MODAL_ID = 'editEntryModal';
  editForm: FormGroup;
  isLoading = false;

  public currentDatabase: Database | null = null;
  public currentEntry: Entry | null = null;
  private destroy$ = new Subject<void>();

  constructor(
    private fb: FormBuilder,
    private databaseService: DatabaseService,
    private entryService: EntryService,
    private modalService: ModalService
  ) {
    this.editForm = this.fb.group({
      timestamp: ['', Validators.required],
      filename: [''],
      custom_fields: this.fb.group({})
    }); 
  }

  ngOnInit(): void {
    this.databaseService.selectedDatabase$
      .pipe(
        takeUntil(this.destroy$),
        filter((db): db is Database => !!db)
      )
      .subscribe(db => {
        this.currentDatabase = db;
        this.initializeForm(db);
        
        if (this.currentEntry) {
            this.patchForm(this.currentEntry);
        }
      });

    this.entryService.selectedEntry$
      .pipe(
        takeUntil(this.destroy$),
        filter((entry): entry is Entry => !!entry && !!this.currentDatabase)
      )
      .subscribe(entry => {
        this.currentEntry = entry;
        this.patchForm(entry);
      });
  }

  private getLocalISOString(date: Date): string {
    const offset = date.getTimezoneOffset();
    const shiftedDate = new Date(date.getTime() - (offset * 60 * 1000));
    return shiftedDate.toISOString().slice(0, 16);
  }

  /**
   * Dynamically builds nested form controls based on the database's schema.
   */
  private initializeForm(db: Database): void {
    const customGroup = this.fb.group({});

    // Add custom fields
    db.custom_fields.forEach(field => {
      if (field.type === 'COORDINATE') {
        customGroup.addControl(
          field.name,
          this.fb.group({
            latitude: [null, [Validators.min(-90), Validators.max(90)]],
            longitude: [null, [Validators.min(-180), Validators.max(180)]]
          })
        );
      } else {
        const defaultValue = field.type === 'BOOLEAN' ? false : '';
        customGroup.addControl(field.name, this.fb.control(defaultValue));
      }
    });

    this.editForm.setControl('custom_fields', customGroup);
  }

  /**
   * Populates the nested form with data from the selected entry.
   */
  private patchForm(entry: Entry): void {
    if (!this.editForm || !this.currentDatabase) return;

    const localDateTime = this.getLocalISOString(new Date(entry.timestamp));

    const patchData: any = {
      timestamp: localDateTime,
      filename: entry.filename ?? '',
      custom_fields: {}
    };

    // Safely map custom fields if they exist
    this.currentDatabase.custom_fields.forEach(field => {
       if (entry.custom_fields && entry.custom_fields[field.name] !== undefined) {
         let val = entry.custom_fields[field.name];
         if (field.type === 'BOOLEAN') {
           val = (val === 1 || val === true);
         } else if (field.type === 'COORDINATE') {
           if (val && typeof val === 'object') {
             val = {
               latitude: val.latitude !== undefined ? val.latitude : null,
               longitude: val.longitude !== undefined ? val.longitude : null
             };
           } else {
             val = { latitude: null, longitude: null };
           }
         }
         patchData.custom_fields[field.name] = val;
       } else if (field.type === 'COORDINATE') {
         patchData.custom_fields[field.name] = { latitude: null, longitude: null };
       }
    });

    this.editForm.patchValue(patchData);
  }

  onSubmit(): void {
    if (this.editForm.invalid || !this.currentDatabase || !this.currentEntry) {
      return;
    }

    this.isLoading = true;
    const formValue = this.editForm.value;

    const sanitizeCoord = (val: any) => {
      if (!val) return null;
      const lat = val.latitude;
      const lng = val.longitude;
      if (lat !== null && lat !== '' && lat !== undefined && lng !== null && lng !== '' && lng !== undefined) {
        const numLat = Number(lat);
        const numLng = Number(lng);
        if (!isNaN(numLat) && !isNaN(numLng)) {
          return { latitude: numLat, longitude: numLng };
        }
      }
      return null;
    };

    // Build the clean update payload without media_fields
    const updates: any = {
        filename: formValue.filename,
        timestamp: new Date(formValue.timestamp).getTime(),
        custom_fields: { ...formValue.custom_fields }
    };

    // Ensure correct data types for the backend
    this.currentDatabase.custom_fields.forEach(field => {
      const val = updates.custom_fields[field.name];
      if (field.type === 'BOOLEAN') {
        updates.custom_fields[field.name] = !!val;
      } else if ((field.type === 'INTEGER' || field.type === 'REAL') && val !== '' && val !== null) {
        updates.custom_fields[field.name] = Number(val);
      } else if (field.type === 'COORDINATE') {
        updates.custom_fields[field.name] = sanitizeCoord(val);
      }
    });

    this.entryService.updateEntry(this.currentDatabase.id, this.currentEntry.id, updates)
      .pipe(
        takeUntil(this.destroy$),
        finalize(() => this.isLoading = false)
      )
      .subscribe(() => {
        this.modalService.close(true); 
      });
  }

  closeModal(): void {
    this.modalService.close(false); 
  }

  trackByFieldId(index: number, field: any): number | string {
    return field.id ?? field.name;
  }

  ngOnDestroy(): void {
    this.destroy$.next();
    this.destroy$.complete();
  }
}