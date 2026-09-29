import { Component, OnDestroy, OnInit, ChangeDetectorRef } from '@angular/core';
import { FormBuilder, FormGroup, Validators } from '@angular/forms';
import { Subject, firstValueFrom } from 'rxjs';
import { takeUntil, filter, switchMap } from 'rxjs/operators';
import { Database, CustomField } from '../../models'; 
import { EntryService } from '../../services/entry.service';
import { DatabaseService } from '../../services/database.service';
import { ModalService } from '../../services/modal.service';
import { isMimeTypeAllowed, getFileAcceptString } from '../../utils/mime-types';
import { NotificationService } from '../../services/notification.service';
import { AuthService } from '../../services/auth.service';
import { extractMetadata } from '../../utils/metadata-extractor';

@Component({
  selector: 'app-upload-entry-modal',
  templateUrl: './upload-entry-modal.component.html',
  styleUrls: ['./upload-entry-modal.component.css'],
  standalone: false,
})
export class UploadEntryModalComponent implements OnInit, OnDestroy {
  public static readonly MODAL_ID = 'uploadEntryModal';
  uploadForm: FormGroup;
  isLoading = false;
  selectedFile: File | null = null;
  public selectedFileName: string | null = null;
  selectedFiles: File[] = [];
  isMultipleUpload = false;
  currentDatabase: Database | null = null;
  private destroy$ = new Subject<void>();
  
  public fileAcceptString: string | null = null;

  // Batch upload progress state
  public uploadProgressIndex = 0;
  public totalUploads = 0;
  public currentUploadingFileName = '';
  public uploadProgressPercent = 0;

  constructor(
    private fb: FormBuilder,
    private databaseService: DatabaseService,
    private entryService: EntryService,
    private modalService: ModalService,
    private notificationService: NotificationService,
    private cdr: ChangeDetectorRef,
    private authService: AuthService
  ) {
    this.uploadForm = this.fb.group({}); 
  }

  ngOnInit(): void {
    this.databaseService.selectedDatabase$
      .pipe(
        takeUntil(this.destroy$),
        filter((db): db is Database => db !== null)
      )
      .subscribe((db: Database) => {
        this.currentDatabase = db;
        this.initializeForm(); 
        this.fileAcceptString = getFileAcceptString(db.content_type);
      });
    
    this.modalService.getModalEvents(UploadEntryModalComponent.MODAL_ID)
      .pipe(takeUntil(this.destroy$))
      .subscribe(event => {
        if (event.action === 'open') {
           this.resetProgressState();
           if (event.data?.files && event.data.files.length > 1) {
              this.handleMultipleFiles(event.data.files);
           } else if (event.data?.files && event.data.files.length === 1) {
              this.handleFile(event.data.files[0]);
           } else if (event.data?.file) {
              this.handleFile(event.data.file);
           } else if (event.data?.droppedFiles && event.data.droppedFiles.length > 1) {
              this.handleMultipleFiles(event.data.droppedFiles);
           } else if (event.data?.droppedFiles && event.data.droppedFiles.length === 1) {
              this.handleFile(event.data.droppedFiles[0]);
           } else if (event.data?.droppedFile) {
              this.handleFile(event.data.droppedFile);
           } else {
              this.resetFileState();
           }
        }
      });
  }

  private resetProgressState(): void {
    this.uploadProgressIndex = 0;
    this.totalUploads = 0;
    this.currentUploadingFileName = '';
    this.uploadProgressPercent = 0;
  }

  private getLocalISOString(date: Date): string {
    const offset = date.getTimezoneOffset();
    const shiftedDate = new Date(date.getTime() - (offset * 60 * 1000));
    return shiftedDate.toISOString().slice(0, 16);
  }

  private initializeForm(): void {
    Object.keys(this.uploadForm.controls).forEach(key => {
      this.uploadForm.removeControl(key);
    });

    this.uploadForm.addControl('timestamp', this.fb.control(this.getLocalISOString(new Date()), Validators.required));
    this.uploadForm.addControl('file', this.fb.control(null, Validators.required));
    
    this.resetFileState(); 

    if (this.currentDatabase) {
      this.currentDatabase.custom_fields.forEach((field: CustomField) => {
        if (field.type === 'COORDINATE') {
          this.uploadForm.addControl(
            field.name,
            this.fb.group({
              latitude: [null, [Validators.min(-90), Validators.max(90)]],
              longitude: [null, [Validators.min(-180), Validators.max(180)]]
            })
          );
        } else {
          const defaultValue = field.type === 'BOOLEAN' ? false : '';
          this.uploadForm.addControl(field.name, this.fb.control(defaultValue));
        }
      });
    }
  }

  private resetFileState(): void {
    this.selectedFile = null;
    this.selectedFileName = null;
    this.selectedFiles = [];
    this.isMultipleUpload = false;
    this.resetProgressState();
    this.uploadForm.get('file')?.setValue(null, { emitEvent: false });
    this.uploadForm.get('timestamp')?.setValidators([Validators.required]);
    this.uploadForm.get('timestamp')?.updateValueAndValidity();
  }

  onFileSelected(event: Event): void {
    const element = event.currentTarget as HTMLInputElement;
    const fileList: FileList | null = element.files;
    
    if (fileList && fileList.length > 0) {
      const file = fileList[0];
      this.handleFile(file);
    }
  }

  onMultipleFilesSelected(event: Event): void {
    const element = event.currentTarget as HTMLInputElement;
    const fileList: FileList | null = element.files;
    if (fileList && fileList.length > 0) {
      const files = Array.from(fileList).filter(f => 
        this.currentDatabase ? isMimeTypeAllowed(this.currentDatabase.content_type, f.type) : true
      );
      if (files.length === 1) {
        this.handleFile(files[0]);
      } else if (files.length > 1) {
        this.handleMultipleFiles(files);
      }
    }
  }

  async handleFile(file: File | null): Promise<void> {
    if (!file) {
      this.resetFileState();
      return;
    }

    if (this.currentDatabase && !isMimeTypeAllowed(this.currentDatabase.content_type, file.type)) {
        this.notificationService.showError(`Invalid file type (${file.type}). Allowed: ${this.currentDatabase.content_type}`);
        return; 
    }

    this.selectedFile = file;
    this.selectedFileName = file.name;
    this.selectedFiles = [];
    this.isMultipleUpload = false;
    
    this.uploadForm.patchValue({ file: this.selectedFile });
    
    this.uploadForm.get('file')?.markAsTouched();
    this.uploadForm.get('file')?.updateValueAndValidity();

    this.uploadForm.get('timestamp')?.setValidators([Validators.required]);
    this.uploadForm.get('timestamp')?.setValue(this.getLocalISOString(new Date()));
    this.uploadForm.get('timestamp')?.updateValueAndValidity();
    
    this.cdr.detectChanges();

    try {
      const ext = await extractMetadata(file);
      if (ext) {
        if (ext.timestamp) {
          this.uploadForm.patchValue({ timestamp: this.getLocalISOString(ext.timestamp) });
          this.notificationService.showSuccess(`Extracted capture timestamp from file: ${ext.timestamp.toLocaleString()}`);
        }
        if (ext.coordinates && this.currentDatabase) {
          const coordFields = this.currentDatabase.custom_fields.filter(cf => cf.type === 'COORDINATE');
          if (coordFields.length > 0) {
            coordFields.forEach(cf => {
              this.uploadForm.get(cf.name)?.patchValue({
                latitude: ext.coordinates!.latitude,
                longitude: ext.coordinates!.longitude
              });
            });
            this.notificationService.showSuccess(
              `Extracted GPS coordinates (${ext.coordinates.latitude.toFixed(6)}, ${ext.coordinates.longitude.toFixed(6)}) from file.`
            );
          }
        }
        this.cdr.detectChanges();
      }
    } catch (err) {
      console.error('[UploadModal] Error auto-extracting metadata:', err);
    }
  }

  handleMultipleFiles(files: File[]): void {
    this.selectedFiles = files;
    this.isMultipleUpload = true;
    this.selectedFile = null;
    this.selectedFileName = null;

    this.uploadForm.patchValue({ file: files[0] });
    this.uploadForm.get('file')?.updateValueAndValidity();

    this.uploadForm.get('timestamp')?.clearValidators();
    this.uploadForm.get('timestamp')?.updateValueAndValidity();
    
    this.cdr.detectChanges();
  }

  onSubmit(): void {
    if (this.uploadForm.invalid || !this.currentDatabase || (!this.selectedFile && this.selectedFiles.length === 0)) {
      this.uploadForm.markAllAsTouched(); 
      return;
    }

    this.isLoading = true;

    // Destructure to separate the core fields from the dynamic custom fields
    const { timestamp, file, ...rawCustomFields } = this.uploadForm.value;

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

    const custom_fields: Record<string, any> = {};

    this.currentDatabase.custom_fields.forEach(field => {
        if (rawCustomFields.hasOwnProperty(field.name)) {
            let value = rawCustomFields[field.name];
            
            if (field.type === 'BOOLEAN') {
                value = !!value; 
            } else if ((field.type === 'INTEGER' || field.type === 'REAL') && value !== '' && value !== null) {
                value = Number(value);
            } else if (field.type === 'COORDINATE') {
                value = sanitizeCoord(value);
            }
            
            if (value !== null && value !== undefined && value !== '') {
              custom_fields[field.name] = value;
            }
        }
    });

    // Pre-emptively ping the server to ensure our access token is fresh. 
    this.authService.fetchCurrentUser().pipe(
      switchMap(async (user) => {
        if (!user) {
          throw new Error('User session is invalid or expired.');
        }

        if (this.isMultipleUpload) {
          this.totalUploads = this.selectedFiles.length;
          this.uploadProgressIndex = 0;
          this.uploadProgressPercent = 0;

          for (let i = 0; i < this.selectedFiles.length; i++) {
            const f = this.selectedFiles[i];
            this.uploadProgressIndex = i + 1;
            this.currentUploadingFileName = f.name;
            this.uploadProgressPercent = Math.round((i / this.selectedFiles.length) * 100);
            this.cdr.detectChanges();

            const ext = await extractMetadata(f);
            const ts = ext?.timestamp ? ext.timestamp.getTime() : Date.now();
            const itemCustomFields: Record<string, any> = { ...custom_fields };

            if (ext?.coordinates && this.currentDatabase) {
              const coordFields = this.currentDatabase.custom_fields.filter(cf => cf.type === 'COORDINATE');
              coordFields.forEach(cf => {
                itemCustomFields[cf.name] = ext.coordinates;
              });
            }

            const metadata = {
              timestamp: ts,
              filename: f.name,
              custom_fields: itemCustomFields
            };
            
            await firstValueFrom(this.entryService.uploadEntry(
              this.currentDatabase!.id, 
              metadata as any, 
              f,
              { silentSuccess: true }
            ));

            this.uploadProgressPercent = Math.round(((i + 1) / this.selectedFiles.length) * 100);
            this.cdr.detectChanges();
          }

          this.notificationService.showSuccess(`Successfully uploaded ${this.selectedFiles.length} entries.`);
        } else {
          const metadata = {
            timestamp: new Date(timestamp).getTime(),
            filename: this.selectedFile!.name,
            custom_fields: custom_fields 
          };
          await firstValueFrom(this.entryService.uploadEntry(this.currentDatabase!.id, metadata as any, this.selectedFile!));
        }
      })
    ).subscribe({
      next: () => {
        this.isLoading = false;
        this.resetProgressState();
        this.closeModal();
      },
      error: (err) => {
        this.isLoading = false;
        this.notificationService.showError(err?.message || 'Upload failed.');
        if (this.isMultipleUpload) {
          this.entryService.triggerImageListRefresh();
        }
      }
    });
  }

  closeModal(): void {
    if (this.isLoading) return;
    this.modalService.close();
    if (this.currentDatabase) {
      this.initializeForm();
    }
  }

  trackByFileName(index: number, file: File): string {
    return file.name;
  }

  trackByFieldId(index: number, field: any): number | string {
    return field.id ?? field.name;
  }

  ngOnDestroy(): void {
    this.destroy$.next();
    this.destroy$.complete();
  }
}