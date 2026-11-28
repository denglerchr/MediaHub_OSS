import { CommonModule } from '@angular/common';
import { Component, OnInit, ChangeDetectorRef, DestroyRef, ViewChild, inject } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { ActivatedRoute, ParamMap } from '@angular/router';
import { Observable, of, Subject, merge } from 'rxjs';
import {
  switchMap,
  finalize,
  filter,
  take,
  map,
  distinctUntilChanged,
  tap,
  catchError,
} from 'rxjs/operators';
import {
  Database,
  User,
  SearchRequest,
  SearchFilter,
  Entry,
  ConfirmationModalData,
} from '../../../core/models';
import { DatabaseService } from '../../../core/services/database.service';
import { EntryService } from '../../../core/services/entry.service';
import { AuthService } from '../../../core/services/auth.service';
import { ModalService } from '../../../core/services/modal.service';
import { NotificationService } from '../../../core/services/notification.service';
import { UploadEntryModalComponent } from '../upload-entry-modal/upload-entry-modal.component';
import { EntryDetailModalComponent } from '../entry-detail-modal/entry-detail-modal.component';
import { EditEntryModalComponent } from '../edit-entry-modal/edit-entry-modal.component';
import {
  FullscreenSettings,
  FullscreenSettingsModalComponent,
} from '../fullscreen-settings-modal/fullscreen-settings-modal.component';
import { PageHeaderComponent } from '../../../shared/components/page-header/page-header.component';
import { EntryGridComponent } from '../entry-grid/entry-grid.component';
import { EntryListViewComponent } from '../entry-list-view/entry-list-view.component';
import { FullscreenPlayerComponent } from '../fullscreen-player/fullscreen-player.component';
import { FileDragDropDirective } from '../../../shared/directives/file-drag-drop.directive';
import { isMimeTypeAllowed, getFileAcceptString } from '../../../shared/utils/mime-types';
import { isReservedCustomFieldName } from '../../../shared/utils/validation';
import { resolveDatabasePermissions } from '../../../shared/utils/permissions';
import {
  canDeleteEntry,
  canEditEntry,
  getMediaFieldsForContentType,
  isEntryProcessing,
} from '../../../shared/utils/entry-domain';
import { downloadBlob } from '../../../shared/utils/download';
import {
  AvailableFilter,
  FilterChangedEvent,
  EntryFilterComponent,
} from '../entry-filter/entry-filter.component';

@Component({
  selector: 'app-entry-list',
  templateUrl: './entry-list.component.html',
  styleUrls: ['./entry-list.component.css'],
  standalone: true,
  imports: [
    CommonModule,
    PageHeaderComponent,
    EntryFilterComponent,
    EntryGridComponent,
    EntryListViewComponent,
    FullscreenPlayerComponent,
    FileDragDropDirective,
  ],
})
export class EntryListComponent implements OnInit {
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

  private readonly destroyRef = inject(DestroyRef);
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
    this.currentUser$.pipe(takeUntilDestroyed(this.destroyRef)).subscribe((user) => {
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
        switchMap((id) => {
          if (this.showFullscreenPlayer) {
            this.closeFullscreenPlayer();
          }
          if (!id) {
            this.dbId = null;
            this.currentDb = null;
            this.fileAcceptString = null;
            this.entriesToShow = [];
            this.tableColumns = [];
            this.availableFilters = [];
            this.loadError = false;
            this.isLoading = false;
            this.updatePermissions();
            this.cdr.markForCheck();
            return of(null);
          }

          this.dbId = id;
          this.entriesToShow = [];
          this.currentPage = 1;
          this.imagesPerPage = 200;
          this.hasNextPage = false;
          this.clearSelection();
          this.currentFilterConditions = undefined;
          this.loadError = false;
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
                this.loadError = true;
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
                    this.loadError = false;
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
        takeUntilDestroyed(this.destroyRef),
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

    this.entryService.entryCreated$
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe((newEntry) => {
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
          if (updated.length > this.imagesPerPage) {
            this.entriesToShow = updated.slice(0, this.imagesPerPage);
            this.hasNextPage = true;
          } else {
            this.entriesToShow = updated;
          }
          this.cdr.markForCheck();
        }
      });

    this.entryService.entryUpdated$
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe((updatedEntry) => {
        if (
          !this.currentDb ||
          (updatedEntry.database_id && updatedEntry.database_id !== this.currentDb.id)
        ) {
          return;
        }
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
    const perms = resolveDatabasePermissions(this.currentUser, this.currentDb ? this.dbId : null);
    this.canView = perms.canView;
    this.canCreate = perms.canCreate;
    this.canEdit = perms.canEdit;
    this.canDelete = perms.canDelete;
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
      this.loadError = false;
      this.routeRetry$.next();
      return;
    }
    this.loadError = false;
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
      { name: 'status', type: 'STATUS' },
    ];

    for (const fieldName of getMediaFieldsForContentType(db.content_type)) {
      filters.push({
        name: fieldName,
        type: fieldName === 'duration' ? 'REAL' : 'INTEGER',
      });
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
    const dbId = this.dbId;
    this.isBulkProcessing = true;
    const ids = Array.from(this.selectedEntryIds);
    this.entryService
      .bulkExportEntries(dbId, ids) // UPDATED
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => (this.isBulkProcessing = false)),
      )
      .subscribe({
        next: (blob) => {
          downloadBlob(blob, `${this.currentDb?.name || 'database'}_export.zip`);
          this.notificationService.showSuccess('Export downloaded successfully.');
          this.clearSelection();
        },
        error: () => {},
      });
  }

  onBulkDelete(): void {
    if (!this.dbId || !this.currentDb || this.selectedEntryIds.size === 0) return; // UPDATED
    const blockedSelected = this.entriesToShow.find(
      (e) => this.selectedEntryIds.has(e.id) && !canDeleteEntry(e),
    );
    if (blockedSelected) {
      return this.notificationService.showError(
        `Cannot delete: entry ${blockedSelected.id} is still ${blockedSelected.status}.`,
      );
    }
    const dbId = this.dbId;
    const ids = Array.from(this.selectedEntryIds);
    const modalData: ConfirmationModalData = {
      title: 'Confirm Bulk Deletion',
      message: `Are you sure you want to delete ${ids.length} selected entries from '${this.currentDb.name}'?`, // Display name
    };
    this.modalService
      .confirm(modalData)
      .pipe(filter(Boolean), takeUntilDestroyed(this.destroyRef))
      .subscribe(() => {
        this.isBulkProcessing = true;
        this.entryService
          .bulkDeleteEntries(dbId, ids) // UPDATED
          .pipe(
            takeUntilDestroyed(this.destroyRef),
            finalize(() => (this.isBulkProcessing = false)),
          )
          .subscribe({
            next: () => this.clearSelection(),
            error: () => {},
          });
      });
  }

  // --- UI Setup & Modals ---

  private setupTableColumns(db: Database): void {
    const standardColumns = [
      'id',
      'timestamp',
      'filename',
      'mime_type',
      'filesize',
      'status',
      ...getMediaFieldsForContentType(db.content_type),
    ];

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

    this.handleIncomingFiles(Array.from(input.files), 'selected');
  }

  openEditModal(entry: Entry): void {
    if (this.dbId) {
      if (!canEditEntry(entry))
        return this.notificationService.showError(`Cannot edit ${entry.status} entry.`);
      this.entryService.selectEntry(entry);
      this.modalService.open(EditEntryModalComponent.MODAL_ID);
    }
  }

  openDeleteConfirm(entry: Entry): void {
    if (!this.currentDb) return;
    if (!canDeleteEntry(entry))
      return this.notificationService.showError(`Cannot delete ${entry.status} entry.`);
    const dbId = this.currentDb.id;
    const modalData: ConfirmationModalData = { message: `Delete entry ${entry.id}?` };
    this.modalService
      .confirm(modalData)
      .pipe(filter(Boolean), takeUntilDestroyed(this.destroyRef))
      .subscribe(() => {
        this.entryService
          .deleteEntry(dbId, entry.id)
          .pipe(takeUntilDestroyed(this.destroyRef))
          .subscribe({ error: () => {} });
      });
  }

  openFullscreenSettingsModal(): void {
    if (!this.dbId || !this.currentDb) {
      this.notificationService.showInfo('Please select a database first.');
      return;
    }

    this.modalService
      .open(FullscreenSettingsModalComponent.MODAL_ID)
      .pipe(take(1), takeUntilDestroyed(this.destroyRef))
      .subscribe((result) => {
        if (result) {
          this.fullscreenSettings = result;
          this.showFullscreenPlayer = true;
          this.cdr.detectChanges();
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
      if (isEntryProcessing(entry)) return this.notificationService.showInfo('Entry processing...');
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

    this.handleIncomingFiles(files, 'dropped');
  }

  private handleIncomingFiles(files: File[], source: 'selected' | 'dropped'): void {
    if (!this.currentDb) return;

    const validFiles = files.filter((f) => isMimeTypeAllowed(this.currentDb!.content_type, f.type));
    if (validFiles.length === 0) {
      this.notificationService.showError(
        `No valid files ${source}. Allowed types: ${this.currentDb.content_type}`,
      );
      return;
    }

    if (validFiles.length < files.length) {
      this.notificationService.showInfo(
        `Skipped ${files.length - validFiles.length} file(s) of invalid type.`,
      );
    }

    this.modalService.open(UploadEntryModalComponent.MODAL_ID, { files: validFiles });
  }
}
