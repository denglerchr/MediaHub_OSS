import {
  ChangeDetectionStrategy,
  ChangeDetectorRef,
  Component,
  DestroyRef,
  OnDestroy,
  OnInit,
  inject,
} from '@angular/core';
import { CommonModule } from '@angular/common';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { Subscription } from 'rxjs';
import { finalize } from 'rxjs/operators';
import { DatabaseService } from '../../../core/services/database.service';
import { EntryService } from '../../../core/services/entry.service';
import { AuthService } from '../../../core/services/auth.service';
import { ModalService } from '../../../core/services/modal.service';
import { CreateDatabaseModalComponent } from '../../databases/create-database-modal/create-database-modal.component';
import { Database, User } from '../../../core/models';
import { Router } from '@angular/router';
import { SecureImageDirective } from '../../../shared/directives/secure-image.directive';
import { FormatBytesPipe } from '../../../shared/pipes/format-bytes.pipe';

@Component({
  selector: 'app-overview-page',
  templateUrl: './overview-page.component.html',
  styleUrls: ['./overview-page.component.css'],
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule, SecureImageDirective, FormatBytesPipe],
})
export class OverviewPageComponent implements OnInit, OnDestroy {
  public databases: Database[] = [];
  public currentUser: User | null = null;
  public dbPreviews: Record<string, string> = {};
  public failedPreviews = new Set<string>();
  public adminAccessByDbId: Record<string, boolean> = {};
  public dbIconsById: Record<string, string> = {};

  // FE-034: the entry_count each preview was derived from — databases$ emits on
  // unrelated updates and used to re-issue one searchEntries call per database on
  // every emission. (Accepted tradeoff per the review: two changes that keep the
  // count identical — delete one + upload one — refresh on the next count change.)
  private previewSearchCounts = new Map<string, number>();
  private previewSubs = new Map<string, Subscription>();

  private destroyRef = inject(DestroyRef);

  constructor(
    private databaseService: DatabaseService,
    private entryService: EntryService,
    private authService: AuthService,
    private modalService: ModalService,
    private router: Router,
    private cdr: ChangeDetectorRef,
  ) {}

  ngOnInit(): void {
    this.authService.currentUser$.pipe(takeUntilDestroyed(this.destroyRef)).subscribe((user) => {
      this.currentUser = user;
      this.updateDbMetadata();
      this.cdr.markForCheck();
    });

    this.databaseService.databases$
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe((databases) => {
        this.databases = databases || [];
        this.updateDbMetadata();
        this.pruneRemovedDatabases();
        this.loadPreviews();
        this.cdr.markForCheck();
      });

    this.databaseService
      .loadDatabases()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({ error: () => {} });
  }

  ngOnDestroy(): void {
    for (const sub of this.previewSubs.values()) {
      sub.unsubscribe();
    }
    this.previewSubs.clear();
  }

  private updateDbMetadata(): void {
    const adminAccess: Record<string, boolean> = {};
    const icons: Record<string, string> = {};
    for (const db of this.databases) {
      adminAccess[db.id] = this.hasAdminAccess(db.id);
      icons[db.id] = this.getIconForDb(db);
    }
    this.adminAccessByDbId = adminAccess;
    this.dbIconsById = icons;
  }

  private pruneRemovedDatabases(): void {
    const activeIds = new Set(this.databases.map((db) => db.id));

    for (const dbId of Object.keys(this.dbPreviews)) {
      if (!activeIds.has(dbId)) {
        delete this.dbPreviews[dbId];
      }
    }

    for (const dbId of Array.from(this.previewSearchCounts.keys())) {
      if (!activeIds.has(dbId)) {
        this.previewSearchCounts.delete(dbId);
      }
    }

    for (const dbId of Array.from(this.failedPreviews)) {
      if (!activeIds.has(dbId)) {
        this.failedPreviews.delete(dbId);
      }
    }

    for (const [dbId, sub] of Array.from(this.previewSubs.entries())) {
      if (!activeIds.has(dbId)) {
        sub.unsubscribe();
        this.previewSubs.delete(dbId);
      }
    }
  }

  private cancelPreviewSub(dbId: string): void {
    const existingSub = this.previewSubs.get(dbId);
    if (existingSub) {
      existingSub.unsubscribe();
      this.previewSubs.delete(dbId);
    }
  }

  private loadPreviews(): void {
    for (const db of this.databases) {
      if (!this.authService.hasDatabasePermission(db.id, 'can_view')) {
        this.cancelPreviewSub(db.id);
        continue;
      }

      const entryCount = db.stats?.entry_count ?? 0;
      if (entryCount <= 0) {
        this.cancelPreviewSub(db.id);
        delete this.dbPreviews[db.id];
        this.previewSearchCounts.delete(db.id);
        this.failedPreviews.delete(db.id);
        continue;
      }

      // FE-034: only re-query the latest entry when the count actually changed.
      if (this.previewSearchCounts.get(db.id) === entryCount) {
        continue;
      }
      this.previewSearchCounts.set(db.id, entryCount);

      this.cancelPreviewSub(db.id);

      let sub: Subscription | null = null;
      const subscribedDbId = db.id;
      const isStale = (): boolean =>
        (sub !== null && this.previewSubs.get(subscribedDbId) !== sub) ||
        !this.databases.some((d) => d.id === subscribedDbId);

      // Query the latest entry to use as preview
      sub = this.entryService
        .searchEntries(
          db.id,
          {
            pagination: { limit: 1 },
            sort: { field: 'timestamp', direction: 'desc' },
          },
          { silent: true },
        )
        .pipe(
          takeUntilDestroyed(this.destroyRef),
          finalize(() => {
            if (sub && this.previewSubs.get(subscribedDbId) === sub) {
              this.previewSubs.delete(subscribedDbId);
            }
          }),
        )
        .subscribe({
          next: (entries) => {
            if (isStale()) {
              return;
            }
            if (entries && entries.length > 0) {
              const latestEntry = entries[0];
              this.dbPreviews[db.id] = this.entryService.getEntryPreviewUrl(db.id, latestEntry.id);
              this.failedPreviews.delete(db.id);
            }
            this.cdr.markForCheck();
          },
          error: (err) => {
            if (isStale()) {
              return;
            }
            this.previewSearchCounts.delete(db.id);
            console.error(`Failed to load latest entry for database ${db.name}`, err);
            this.cdr.markForCheck();
          },
        });

      if (!sub.closed) {
        this.previewSubs.set(db.id, sub);
      }
    }
  }

  public getIconForDb(db: Database): string {
    switch (db.content_type) {
      case 'image':
        return 'assets/icons/image-icon.svg';
      case 'audio':
        return 'assets/icons/audio-icon.svg';
      case 'video':
        return 'assets/icons/video-icon.svg';
      case 'file':
        return 'assets/icons/file-icon.svg';
      default:
        return 'assets/icons/db-icon.svg';
    }
  }

  /** FE-028: stable trackBy key for the database cards. */
  public trackByDbId(index: number, db: Database): string {
    return db.id;
  }

  public handleImageError(dbId: string): void {
    this.failedPreviews.add(dbId);
    this.cdr.markForCheck();
  }

  public openCreateDatabaseModal(): void {
    this.modalService.open(CreateDatabaseModalComponent.MODAL_ID);
  }

  public goToDatabase(dbId: string): void {
    this.router.navigate(['/dashboard/db', dbId]);
  }

  public goToSettings(event: MouseEvent, dbId: string): void {
    event.preventDefault();
    event.stopPropagation();
    this.router.navigate(['/dashboard/settings', dbId]);
  }

  public hasAdminAccess(dbId: string): boolean {
    return this.authService.hasDatabasePermission(dbId, 'can_admin');
  }
}
