import { Component, OnDestroy, OnInit, ChangeDetectorRef, ViewChild } from '@angular/core';
import { ActivatedRoute, ParamMap } from '@angular/router';
import { Observable, of, Subject, merge } from 'rxjs';
import {
  switchMap,
  takeUntil,
  finalize,
  filter,
  take,
  map,
  distinctUntilChanged,
  tap,
  catchError,
} from 'rxjs/operators';
import { Database, User, SearchRequest, SearchFilter, Entry } from '../../models';
import { DatabaseService } from '../../services/database.service';
import { EntryService } from '../../services/entry.service';
import { AuthService } from '../../services/auth.service';
import { ModalService } from '../../services/modal.service';
import { NotificationService } from '../../services/notification.service';
import { UploadEntryModalComponent } from '../upload-entry-modal/upload-entry-modal.component';
import { EntryDetailModalComponent } from '../entry-detail-modal/entry-detail-modal.component';
import { EditEntryModalComponent } from '../edit-entry-modal/edit-entry-modal.component';
import {
  ConfirmationModalComponent,
  ConfirmationModalData,
} from '../confirmation-modal/confirmation-modal.component';
import {
  FullscreenSettings,
  FullscreenSettingsModalComponent,
} from '../fullscreen-settings-modal/fullscreen-settings-modal.component';
import { isMimeTypeAllowed, getFileAcceptString } from '../../utils/mime-types';
import { isReservedCustomFieldName } from '../../utils/validation';
import {
  AvailableFilter,
  FilterChangedEvent,
  EntryFilterComponent,
} from '../entry-filter/entry-filter.component';

@Component({
  selector: 'app-entry-list',
  templateUrl: './entry-list.component.html',
  styleUrls: ['./entry-list.component.css'],
  standalone: false,
})
export class EntryListComponent implements OnInit, OnDestroy {
  // FE-015: the filter bar is gated behind can_view (*ngIf), so it cannot be a
  // plain template-ref — its scope would change. A ViewChild keeps the header
  // button working either way (it is hidden together with the bar).
  @ViewChild(EntryFilterComponent) entryFilter?: EntryFilterComponent;

  public currentUser$: Observable<User | null>;
  public entriesToShow: Entry[] = [];
  public currentUser: User | null = null;

  public isLoading = true;
  // FE-012: distinguishes "the search failed" from "no results match the filters".
  public loadError = false;
  public isBulkProcessing = false;
  public tableColumns: string[] = [];
  public availableFilters: AvailableFilter[] = [];

  public dbId: string | null = null; // UPDATED: dbName -> dbId
  public currentDb: Database | null = null;
  public fileAcceptString: string | null = null;

  // Scoped permission flags
  // FE-015: `can_view` gates the entries list / filters / search; the upload UI is
  // reachable with `can_create` alone (upload-only accounts).
  public canView = false;
  public canCreate = false;
  public canEdit = false;
  public canDelete = false;

  private destroy$ = new Subject<void>();
  private manualFetchTrigger$ = new Subject<void>();
  // FE-012: re-runs the whole per-database chain after a failed database lookup.
  private routeRetry$ = new Subject<void>();

  // --- STATE PROPERTIES ---
  public viewMode: 'grid' | 'list' = 'grid';
  public currentPage = 1;
  public imagesPerPage = 200;
  public hasNextPage = false;

  private currentFilterConditions: SearchFilter | undefined;

  // --- SELECTION STATE ---
  public selectedEntryIds = new Set<number>();
  public lastSelectedEntryId: number | null = null;

  // --- FULLSCREEN PLAYER STATE ---
  public showFullscreenPlayer = false;
  // FE-052: typed instead of smuggling the settings through `as any`.
  public fullscreenSettings: FullscreenSettings | null = null;

  constructor(
    private route: ActivatedRoute,
    private databaseService: DatabaseService,
    private entryService: EntryService,
    private authService: AuthService,
    private modalService: ModalService,
    private notificationService: NotificationService,
    private cdr: ChangeDetectorRef,
  ) {
    this.currentUser$ = this.authService.currentUser$;
  }

  ngOnInit(): void {
    this.currentUser$.pipe(takeUntil(this.destroy$)).subscribe((user) => {
      this.currentUser = user;
      this.updatePermissions();
      if (this.currentDb) {
        this.setupTableColumns(this.currentDb);
      }
    });

    merge(
      this.route.paramMap.pipe(
        map((params: ParamMap) => params.get('id')),
        distinctUntilChanged(),
      ),
      // FE-012: a retry re-runs the chain for the current id (e.g. after a failed
      // database lookup).
      this.routeRetry$.pipe(map(() => this.dbId)),
    )
      .pipe(
        takeUntil(this.destroy$),
        switchMap((id) => {
          if (!id) {
            this.dbId = null;
            this.currentDb = null;
            this.fileAcceptString = null;
            this.entriesToShow = [];
            this.tableColumns = [];
            this.availableFilters = [];
            this.isLoading = false;
            this.updatePermissions();
            this.cdr.markForCheck();
            return of(null);
          }

          this.dbId = id;
          this.entriesToShow = [];
          this.currentPage = 1;
          this.hasNextPage = false;
          this.clearSelection();
          this.currentFilterConditions = undefined;
          this.isLoading = true;
          this.cdr.markForCheck();

          return this.databaseService.selectDatabase(id).pipe(
            // FE-012: a failed database lookup must not terminate the route pipeline.
            catchError(() => {
              this.loadError = true;
              this.isLoading = false;
              this.cdr.markForCheck();
              return of(null);
            }),
            tap((db) => {
              if (db) {
                this.currentDb = db;
                this.fileAcceptString = getFileAcceptString(db.content_type);
                this.updatePermissions();
                this.setupTableColumns(db);
                this.setupAvailableFilters(db);
              } else {
                this.currentDb = null;
                this.fileAcceptString = null;
                this.updatePermissions();
                this.tableColumns = [];
                this.availableFilters = [];
              }
              this.cdr.markForCheck();
            }),
            switchMap((db) => {
              if (!db) {
                this.isLoading = false;
                this.cdr.markForCheck();
                return of(null);
              }

              return merge(
                of({ isSilent: false }),
                this.manualFetchTrigger$.pipe(map(() => ({ isSilent: false }))),
                this.entryService.refreshRequired$.pipe(map(() => ({ isSilent: true }))),
              ).pipe(
                tap(({ isSilent }) => {
                  if (!isSilent) {
                    this.isLoading = true;
                    this.clearSelection();
                  }
                  this.cdr.markForCheck();
                }),
                switchMap(() => {
                  // FE-015: searching entries requires CanView. A user who only holds
                  // e.g. CanCreate must not get a 403 here — the list UI is hidden and
                  // only the upload affordances are offered.
                  if (this.currentUser && !this.canView) {
                    this.isLoading = false;
                    this.entriesToShow = [];
                    this.hasNextPage = false;
                    this.cdr.markForCheck();
                    return of([] as Entry[]);
                  }
                  const searchPayload = this.buildSearchPayload();
                  // FE-012: keep the error inside the inner stream — one failed search
                  // must never kill the (long-lived) route subscription. `null` marks
                  // the error so `next` below does not treat it as an empty result.
                  return this.entryService.searchEntries(id, searchPayload).pipe(
                    catchError(() => {
                      this.loadError = true;
                      this.entriesToShow = [];
                      this.hasNextPage = false;
                      this.isLoading = false;
                      return of(null);
                    }),
                  );
                }),
              );
            }),
          );
        }),
      )
      .subscribe({
        next: (entries) => {
          if (!entries) return;
          if (entries.length > this.imagesPerPage) {
            this.hasNextPage = true;
            this.entriesToShow = entries.slice(0, this.imagesPerPage);
          } else {
            this.hasNextPage = false;
            this.entriesToShow = entries;
          }
          this.loadError = false;
          this.isLoading = false;
          this.cdr.markForCheck();
        },
        error: (err) => {
          // FE-012: defence in depth — errors are contained in the inner streams above,
          // this only fires if something outside them fails.
          this.isLoading = false;
          this.loadError = true;
          this.entriesToShow = [];
          this.hasNextPage = false;
          this.cdr.markForCheck();
        },
      });

    this.entryService.entryCreated$.pipe(takeUntil(this.destroy$)).subscribe((newEntry) => {
      // FE-021: only splice a freshly created entry into the *first page of an
      // unfiltered* view of its own database. On page > 1 the offset pagination
      // would shift rows (duplicates on Next/Previous), and with a filter active
      // the entry may not match the filter at all. Those views get a silent
      // server-side refetch instead (the search contract is authoritative there).
      if (!this.currentDb || newEntry.database_id !== this.currentDb.id) {
        return;
      }
      if (this.currentPage !== 1 || this.currentFilterConditions) {
        this.entryService.triggerImageListRefresh();
        return;
      }
      if (!this.entriesToShow.some((e) => e.id === newEntry.id)) {
        const updated = [newEntry, ...this.entriesToShow];
        updated.sort((a, b) => (b.timestamp || 0) - (a.timestamp || 0));
        this.entriesToShow = updated;
        this.cdr.markForCheck();
      }
    });

    this.entryService.entryUpdated$.pipe(takeUntil(this.destroy$)).subscribe((updatedEntry) => {
      const index = this.entriesToShow.findIndex((e) => e.id === updatedEntry.id);
      if (index !== -1) {
        this.entriesToShow[index] = { ...this.entriesToShow[index], ...updatedEntry };
        this.entriesToShow = [...this.entriesToShow];
        this.cdr.markForCheck();
      }
    });
  }

  // Calculates permissions for the currently selected database
  private updatePermissions(): void {
    if (!this.currentUser || !this.currentDb) {
      this.canView = false;
      this.canCreate = false;
      this.canEdit = false;
      this.canDelete = false;
      return;
    }

    if (this.currentUser.is_admin) {
      this.canView = true;
      this.canCreate = true;
      this.canEdit = true;
      this.canDelete = true;
    } else {
      // UPDATED: check database_id matches currentDb.id
      const dbPermission = this.currentUser.permissions?.find(
        (p) => p.database_id === this.currentDb!.id,
      );
      this.canView = dbPermission?.can_view || false;
      this.canCreate = dbPermission?.can_create || false;
      this.canEdit = dbPermission?.can_edit || false;
      this.canDelete = dbPermission?.can_delete || false;
    }
  }

  onFilterApplied(event: FilterChangedEvent): void {
    this.currentFilterConditions = event.filter;
    this.imagesPerPage = event.limit;
    this.currentPage = 1;
    this.manualFetchTrigger$.next();
  }

  /** FE-015/FE-027: header button → collapse toggle of the (can_view-gated) filter bar. */
  toggleFilters(): void {
    this.entryFilter?.toggleCollapse();
  }

  /**
   * FE-012: recovers from a failed load without touching filters/page. A failed
   * search is retried in place; a failed database lookup re-runs the whole chain.
   */
  retryLoad(): void {
    if (this.loadError && !this.currentDb && this.dbId) {
      this.routeRetry$.next();
      return;
    }
    this.manualFetchTrigger$.next();
  }

  private buildSearchPayload(): SearchRequest {
    const payload: SearchRequest = {
      pagination: {
        limit: this.imagesPerPage + 1,
        offset: (this.currentPage - 1) * this.imagesPerPage,
      },
      sort: { field: 'timestamp', direction: 'desc' },
    };

    if (this.currentFilterConditions) {
      payload.filter = this.currentFilterConditions;
    }
    return payload;
  }

  private setupAvailableFilters(db: Database): void {
    const filters: AvailableFilter[] = [
      { name: 'timestamp', type: 'INTEGER' },
      { name: 'filesize', type: 'INTEGER' },
      { name: 'mime_type', type: 'TEXT' },
      { name: 'filename', type: 'TEXT' },
      { name: 'status', type: 'TEXT' },
    ];

    if (db.content_type === 'image') {
      filters.push({ name: 'width', type: 'INTEGER' });
      filters.push({ name: 'height', type: 'INTEGER' });
    } else if (db.content_type === 'audio') {
      filters.push({ name: 'duration', type: 'REAL' });
      filters.push({ name: 'channels', type: 'INTEGER' });
    } else if (db.content_type === 'video') {
      filters.push({ name: 'width', type: 'INTEGER' });
      filters.push({ name: 'height', type: 'INTEGER' });
      filters.push({ name: 'duration', type: 'REAL' });
    }

    // FE-049: never offer a filter for a name that collides with a standard/media
    // field — it would target the wrong column (see setupTableColumns).
    filters.push(
      ...db.custom_fields.filter(
        (field) =>
          !filters.some((existing) => existing.name === field.name) &&
          !isReservedCustomFieldName(field.name),
      ),
    );
    filters.sort((a, b) => a.name.localeCompare(b.name));
    this.availableFilters = filters;
  }

  // --- SELECTION LOGIC ---

  toggleSelection(event: { entry: Entry; event: MouseEvent }): void {
    const { entry, event: mouseEvent } = event;
    const entryId = entry.id;

    if (mouseEvent.shiftKey) {
      const selection = window.getSelection();
      if (selection) {
        selection.removeAllRanges();
      }
    }

    const newSelection = new Set(this.selectedEntryIds);

    if (mouseEvent.shiftKey && this.lastSelectedEntryId !== null) {
      const lastIndex = this.entriesToShow.findIndex((e) => e.id === this.lastSelectedEntryId);
      const currentIndex = this.entriesToShow.findIndex((e) => e.id === entryId);

      if (lastIndex !== -1 && currentIndex !== -1) {
        const start = Math.min(lastIndex, currentIndex);
        const end = Math.max(lastIndex, currentIndex);
        for (let i = start; i <= end; i++) {
          newSelection.add(this.entriesToShow[i].id);
        }
      }
    } else {
      if (newSelection.has(entryId)) {
        newSelection.delete(entryId);
        this.lastSelectedEntryId = null;
      } else {
        newSelection.add(entryId);
        this.lastSelectedEntryId = entryId;
      }
    }

    this.selectedEntryIds = newSelection;
  }

  clearSelection(): void {
    this.selectedEntryIds = new Set<number>();
    this.lastSelectedEntryId = null;
  }

  // --- BULK ACTIONS ---

  onBulkDownload(): void {
    if (!this.dbId || this.selectedEntryIds.size === 0) return; // UPDATED
    this.isBulkProcessing = true;
    const ids = Array.from(this.selectedEntryIds);
    this.entryService
      .bulkExportEntries(this.dbId, ids) // UPDATED
      .pipe(finalize(() => (this.isBulkProcessing = false)))
      .subscribe({
        next: (blob) => {
          const url = window.URL.createObjectURL(blob);
          const a = document.createElement('a');
          a.href = url;
          a.download = `${this.currentDb?.name || 'database'}_export.zip`; // Display name for file download
          document.body.appendChild(a);
          a.click();
          document.body.removeChild(a);
          window.URL.revokeObjectURL(url);
          this.notificationService.showSuccess('Export downloaded successfully.');
          this.clearSelection();
        },
        error: () => this.notificationService.showError('Failed to generate export archive.'),
      });
  }

  onBulkDelete(): void {
    if (!this.dbId || !this.currentDb || this.selectedEntryIds.size === 0) return; // UPDATED
    const queuedSelected = this.entriesToShow.find(
      (e) => this.selectedEntryIds.has(e.id) && e.status === 'queued',
    );
    if (queuedSelected) {
      return this.notificationService.showError(
        'Cannot delete: entry ' + queuedSelected.id + ' is still queued.',
      );
    }
    const ids = Array.from(this.selectedEntryIds);
    const modalData: ConfirmationModalData = {
      title: 'Confirm Bulk Deletion',
      message: `Are you sure you want to delete ${ids.length} selected entries from '${this.currentDb.name}'?`, // Display name
    };
    this.modalService
      .open(ConfirmationModalComponent.MODAL_ID, modalData)
      .pipe(
        take(1),
        filter((confirmed) => confirmed === true),
      )
      .subscribe(() => {
        if (this.dbId) {
          this.isBulkProcessing = true;
          this.entryService
            .bulkDeleteEntries(this.dbId, ids) // UPDATED
            .pipe(finalize(() => (this.isBulkProcessing = false)))
            .subscribe({ next: () => this.clearSelection() });
        }
      });
  }

  // --- UI Setup & Modals ---

  private setupTableColumns(db: Database): void {
    const standardColumns = ['id', 'timestamp', 'filename', 'mime_type', 'filesize', 'status'];

    if (db.content_type === 'image') standardColumns.push('width', 'height');
    else if (db.content_type === 'audio') standardColumns.push('duration', 'channels');
    else if (db.content_type === 'video') standardColumns.push('width', 'height', 'duration');

    // FE-049: a custom field colliding with a standard/media column would render the
    // column twice (duplicate trackBy keys) and bind the wrong values. The backend
    // now reserves these names in AddField, but older databases can still carry such
    // fields — drop the collisions defensively.
    const customColumns = db.custom_fields
      .map((field) => field.name)
      .filter((name) => !standardColumns.includes(name) && !isReservedCustomFieldName(name));
    const actionColumn = this.canEdit || this.canDelete ? ['actions'] : [];

    this.tableColumns = ['Preview', ...standardColumns, ...customColumns, ...actionColumn];
  }

  openUploadModal(fileInput?: HTMLInputElement): void {
    if (!this.dbId || !this.currentDb) {
      this.notificationService.showInfo('Please select a database first.');
      return;
    }
    if (!this.canCreate) {
      this.notificationService.showInfo('Cannot upload here.');
      return;
    }
    if (fileInput) {
      fileInput.value = '';
      fileInput.click();
    } else {
      this.modalService.open(UploadEntryModalComponent.MODAL_ID);
    }
  }

  onFabFileSelected(event: Event): void {
    const input = event.target as HTMLInputElement;
    if (!input.files || input.files.length === 0 || !this.currentDb) {
      return;
    }

    const files = Array.from(input.files);
    const validFiles = files.filter((f) => isMimeTypeAllowed(this.currentDb!.content_type, f.type));

    if (validFiles.length === 0) {
      this.notificationService.showError(
        `No valid files selected. Allowed types: ${this.currentDb.content_type}`,
      );
      return;
    }

    if (validFiles.length < files.length) {
      this.notificationService.showInfo(
        `Skipped ${files.length - validFiles.length} file(s) of invalid type.`,
      );
    }

    if (validFiles.length === 1) {
      this.modalService.open(UploadEntryModalComponent.MODAL_ID, { file: validFiles[0] });
    } else {
      this.modalService.open(UploadEntryModalComponent.MODAL_ID, { files: validFiles });
    }
  }

  openEditModal(entry: Entry): void {
    if (this.dbId) {
      if (entry.status === 'processing')
        return this.notificationService.showError('Cannot edit processing entry.');
      this.entryService.selectEntry(entry);
      this.modalService.open(EditEntryModalComponent.MODAL_ID);
    }
  }

  openDeleteConfirm(entry: Entry): void {
    if (!this.currentDb) return;
    if (entry.status === 'processing')
      return this.notificationService.showError('Cannot delete processing entry.');
    if (entry.status === 'queued')
      return this.notificationService.showError('Cannot delete queued entry.');
    const modalData = { message: `Delete entry ${entry.id}?` };
    this.modalService
      .open(ConfirmationModalComponent.MODAL_ID, modalData)
      .pipe(
        take(1),
        filter((c) => c === true),
      )
      .subscribe(() => {
        this.entryService
          .deleteEntry(this.currentDb!.id, entry.id)
          .pipe(takeUntil(this.destroy$))
          .subscribe();
      });
  }

  openFullscreenSettingsModal(): void {
    if (!this.dbId || !this.currentDb) {
      this.notificationService.showInfo('Please select a database first.');
      return;
    }

    this.modalService
      .open<FullscreenSettings>(FullscreenSettingsModalComponent.MODAL_ID)
      .pipe(take(1))
      .subscribe((result) => {
        if (result) {
          setTimeout(() => {
            this.fullscreenSettings = result;
            this.showFullscreenPlayer = true;
            this.cdr.detectChanges();
          }, 0);
        }
      });
  }

  closeFullscreenPlayer(): void {
    this.showFullscreenPlayer = false;
    this.fullscreenSettings = null;
    this.cdr.markForCheck();
  }

  openDetailModal(entry: Entry): void {
    if (this.dbId) {
      if (entry.status === 'processing')
        return this.notificationService.showInfo('Entry processing...');
      this.entryService.selectEntry(entry);
      this.modalService.open(EntryDetailModalComponent.MODAL_ID);
    }
  }

  public setViewMode(mode: 'grid' | 'list'): void {
    this.viewMode = mode;
    this.cdr.markForCheck();
  }

  public nextPage(): void {
    if (this.hasNextPage) {
      this.currentPage++;
      this.manualFetchTrigger$.next();
    }
  }
  public prevPage(): void {
    if (this.currentPage > 1) {
      this.currentPage--;
      this.manualFetchTrigger$.next();
    }
  }

  onFilesDropped(files: File[]): void {
    if (!this.currentDb || !this.canCreate) {
      this.notificationService.showInfo('Cannot upload here.');
      return;
    }

    const validFiles = files.filter((f) => isMimeTypeAllowed(this.currentDb!.content_type, f.type));
    if (validFiles.length === 0) {
      this.notificationService.showError(
        `No valid files dropped. Allowed types: ${this.currentDb.content_type}`,
      );
      return;
    }

    if (validFiles.length < files.length) {
      this.notificationService.showInfo(
        `Skipped ${files.length - validFiles.length} files of invalid type.`,
      );
    }

    if (validFiles.length === 1) {
      this.modalService.open(UploadEntryModalComponent.MODAL_ID, { droppedFile: validFiles[0] });
    } else {
      this.modalService.open(UploadEntryModalComponent.MODAL_ID, { droppedFiles: validFiles });
    }
  }

  ngOnDestroy(): void {
    this.destroy$.next();
    this.destroy$.complete();
  }
}
