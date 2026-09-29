import { Component, OnDestroy, OnInit } from '@angular/core';
import { Subject, Observable, of, combineLatest } from 'rxjs';
import { switchMap, takeUntil, map, take, filter, tap, withLatestFrom } from 'rxjs/operators';
import { Database, Entry, User } from '../../models';
import { EntryService } from '../../services/entry.service';
import { DatabaseService } from '../../services/database.service';
import { ModalService } from '../../services/modal.service';
import { DomSanitizer, SafeUrl } from '@angular/platform-browser';
import { AuthService } from '../../services/auth.service';
import { NotificationService } from '../../services/notification.service';
import { ConfirmationModalComponent, ConfirmationModalData } from '../confirmation-modal/confirmation-modal.component';
import { EditEntryModalComponent } from '../edit-entry-modal/edit-entry-modal.component';
import { isMimeTypeStreamable } from '../../utils/mime-types';

@Component({
  selector: 'app-entry-detail-modal',
  templateUrl: './entry-detail-modal.component.html',
  styleUrls: ['./entry-detail-modal.component.css'],
  standalone: false,
})
export class EntryDetailModalComponent implements OnInit, OnDestroy {
  public static readonly MODAL_ID = 'entryDetailModal';

  public entryForMetadata: Entry | null = null;
  public fileUrl: SafeUrl | null = null;
  public isLoadingFile: boolean = true;
  
  public currentUser$: Observable<User | null>;
  public previewUrl: string | null = null;
  
  public currentDatabase: Database | null = null; 
  
  public canEdit = false;
  public canDelete = false;

  private currentObjectUrl: string | null = null;
  private destroy$ = new Subject<void>();

  constructor(
    private modalService: ModalService,
    private entryService: EntryService,
    private databaseService: DatabaseService,
    private sanitizer: DomSanitizer,
    private authService: AuthService,
    private notificationService: NotificationService
  ) {
    this.currentUser$ = this.authService.currentUser$;
  }

  ngOnInit(): void {
    // Keep internal state updated
    combineLatest([
      this.databaseService.selectedDatabase$,
      this.currentUser$
    ]).pipe(takeUntil(this.destroy$))
      .subscribe(([db, user]) => {
        this.currentDatabase = db;
        this.updatePermissions(user, db);
      });

    this.entryService.selectedEntry$.pipe(
      takeUntil(this.destroy$),
      withLatestFrom(this.databaseService.selectedDatabase$),
      switchMap(([entry, currentDb]) => {
        this.revokeFileObjectUrl();
        
        this.fileUrl = null;
        this.previewUrl = null;
        this.isLoadingFile = true;
        
        // Pre-fill the metadata with the shallow entry from the list.
        // This gives the HTML template immediate access to 'entry.mime_type',
        this.entryForMetadata = entry; 

        if (entry && currentDb) {
          // UPDATED: Pass currentDb.id instead of currentDb.name
          return this.entryService.getEntryMeta(currentDb.id, entry.id).pipe(
            switchMap(metaEntry => {
              // The HTTP request finished! Overwrite the shallow entry with the full metadata
              this.entryForMetadata = metaEntry;
              this.previewUrl = this.getPreviewUrl(currentDb.id, metaEntry.id); 

              // 1. Check our central utility to see if the browser supports it
              const mime = metaEntry.mime_type || 'file';
              const isStreamable = isMimeTypeStreamable(mime);

              if (isStreamable) {
                // STREAMING SUPPORTED: Provide direct URL
                const streamUrl = this.entryService.getEntryFileUrl(currentDb.id, entry.id); 
                this.fileUrl = this.sanitizer.bypassSecurityTrustUrl(streamUrl);
                this.isLoadingFile = false;
                
                return of('streaming_ready');
                
              } else if (mime.startsWith('image/')) {
                // IMAGES: Fall back to Blob download
                return this.entryService.getEntryFileBlob(currentDb.id, entry.id).pipe( 
                  map(blob => {
                    this.currentObjectUrl = URL.createObjectURL(blob);
                    this.fileUrl = this.sanitizer.bypassSecurityTrustUrl(this.currentObjectUrl);
                    this.isLoadingFile = false;
                    return 'loaded';
                  }),
                  tap({ error: () => this.isLoadingFile = false })
                );
                
              } else {
                // UNSUPPORTED MEDIA / GENERIC FILES
                this.isLoadingFile = false;
                return of('unsupported_file');
              }
            })
          );
        } else {
          this.isLoadingFile = false;
          return of(null);
        }
      })
    ).subscribe();
  }

  private updatePermissions(user: User | null, db: Database | null): void {
    if (!user || !db) {
      this.canEdit = false;
      this.canDelete = false;
      return;
    }

    if (user.is_admin) {
      this.canEdit = true;
      this.canDelete = true;
    } else {
      // UPDATED: Match against database_id and db.id
      const dbPermission = user.permissions?.find(p => p.database_id === db.id);
      this.canEdit = dbPermission?.can_edit || false;
      this.canDelete = dbPermission?.can_delete || false;
    }
  }

  // UPDATED: Renamed parameter to dbId
  public getPreviewUrl(dbId: string, entryId: number): string {
    return this.entryService.getEntryPreviewUrl(dbId, entryId);
  }

  onEdit(): void {
    if (!this.entryForMetadata) return;

    if (this.entryForMetadata.status === 'processing') {
      this.notificationService.showError('Cannot edit an entry that is still processing.');
      return;
    }

    this.modalService.open(EditEntryModalComponent.MODAL_ID);
  }

  onDelete(): void {
    if (!this.currentDatabase || !this.entryForMetadata) return;

    if (this.entryForMetadata.status === 'processing') {
      this.notificationService.showError('Cannot delete an entry that is still processing.');
      return;
    }

    const modalData: ConfirmationModalData = {
      title: 'Delete Entry',
      message: `Are you sure you want to delete entry ${this.entryForMetadata.id}? This action cannot be undone.`
    };

    this.modalService.open(ConfirmationModalComponent.MODAL_ID, modalData)
      .pipe(
        take(1),
        filter(confirmed => confirmed === true)
      )
      .subscribe(() => {
        this.isLoadingFile = true; 
        
        this.entryService.deleteEntry(this.currentDatabase!.id, this.entryForMetadata!.id)
          .pipe(takeUntil(this.destroy$))
          .subscribe({
            next: () => {
              this.closeModal();
            },
            error: () => {
              this.isLoadingFile = false;
            }
          });
      });
  }

  trackByFieldId(index: number, field: any): number | string {
    return field.id ?? field.name;
  }

  hasCoordinate(val: any): boolean {
    return (
      val !== null &&
      val !== undefined &&
      typeof val === 'object' &&
      typeof val.latitude === 'number' &&
      typeof val.longitude === 'number' &&
      !isNaN(val.latitude) &&
      !isNaN(val.longitude)
    );
  }

  formatCoordinate(val: any): string {
    if (!this.hasCoordinate(val)) return 'N/A';
    return `${val.latitude.toFixed(6)}, ${val.longitude.toFixed(6)}`;
  }

  getOpenStreetMapUrl(val: any): string {
    if (!this.hasCoordinate(val)) return '#';
    return `https://www.openstreetmap.org/?mlat=${val.latitude}&mlon=${val.longitude}#map=16/${val.latitude}/${val.longitude}`;
  }

  private revokeFileObjectUrl(): void {
    if (this.currentObjectUrl) {
      URL.revokeObjectURL(this.currentObjectUrl);
      this.currentObjectUrl = null;
    }
  }

  closeModal(): void {
    this.modalService.close();
    this.entryService.clearSelectedEntry();
    this.revokeFileObjectUrl();

    this.fileUrl = null;
    this.previewUrl = null;
    this.isLoadingFile = true;
    this.entryForMetadata = null;
  }

  ngOnDestroy(): void {
    this.destroy$.next();
    this.destroy$.complete();
    this.revokeFileObjectUrl();
  }
}