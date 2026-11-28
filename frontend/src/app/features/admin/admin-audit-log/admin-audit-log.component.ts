// frontend/src/app/components/admin-audit-log/admin-audit-log.component.ts

import { CommonModule } from '@angular/common';
import { Component, OnInit, OnDestroy, ChangeDetectorRef, DestroyRef, inject } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormsModule } from '@angular/forms';
import { Subject, of, timer } from 'rxjs';
import { finalize, switchMap, catchError, map } from 'rxjs/operators';
import { DatetimeDefaultDirective } from '../../../shared/directives/datetime-default.directive';
import { AuditService } from '../../../core/services/audit.service';
import { NotificationService } from '../../../core/services/notification.service';
import { AuditLog } from '../../../core/models/audit.models';
import { parseDatetimeLocalToMs } from '../../../shared/utils/datetime';
import { PageHeaderComponent } from '../../../shared/components/page-header/page-header.component';

/** FE-027: display row — `detailsText` is precomputed once per fetch, not per CD cycle. */
interface AuditLogRow extends AuditLog {
  detailsText: string;
}

@Component({
  selector: 'app-admin-audit-log',
  templateUrl: './admin-audit-log.component.html',
  styleUrls: ['./admin-audit-log.component.css'],
  standalone: true,
  imports: [CommonModule, FormsModule, PageHeaderComponent, DatetimeDefaultDirective],
})
export class AdminAuditLogComponent implements OnInit, OnDestroy {
  // FE-027: template iterates precomputed rows.
  public logRows: AuditLogRow[] = [];
  public isLoading = true;

  // Pagination State
  public limit = 20;
  public offset = 0;
  public currentPage = 1;
  public hasNextPage = false;

  // Filter State (Bound to HTML inputs)
  public startDate: string = '';
  public endDate: string = '';

  public isFilterCollapsed = true;

  // FE-027: precomputed on date changes instead of a per-CD getter.
  public activeFiltersCount = 0;

  public toggleFilter(): void {
    this.isFilterCollapsed = !this.isFilterCollapsed;
  }

  private readonly destroyRef = inject(DestroyRef);
  private loadTrigger$ = new Subject<{ mode: 'immediate' | 'debounced'; resetPage: boolean }>();

  constructor(
    private auditService: AuditService,
    private notificationService: NotificationService,
    private cdr: ChangeDetectorRef,
  ) {}

  ngOnInit(): void {
    // FE-048: route debounced date changes and manual actions through one trigger
    // so manual refresh/clear/pagination immediately cancels any pending 400ms debounce.
    this.loadTrigger$
      .pipe(
        switchMap((req) => (req.mode === 'debounced' ? timer(400) : of(0)).pipe(map(() => req))),
        switchMap((req) => {
          const parsed = this.parseAndValidateDateRange();
          if (!parsed) {
            this.isLoading = false;
            this.cdr.markForCheck();
            return of(null);
          }

          if (req.resetPage) {
            this.offset = 0;
            this.currentPage = 1;
          }

          this.isLoading = true;
          this.cdr.markForCheck();

          // FE-047: ask for one extra row — it is the "is there a next page?" signal.
          // (Fetching exactly `limit` made a total that is a multiple of the page size
          // show an empty trailing page.)
          return this.auditService
            .getAuditLogs(this.limit + 1, this.offset, 'desc', parsed.tstart, parsed.tend)
            .pipe(
              catchError((err) => {
                console.error('Failed to load audit logs', err);
                this.logRows = [];
                this.hasNextPage = false;
                return of(null);
              }),
              finalize(() => {
                this.isLoading = false;
                this.cdr.markForCheck();
              }),
            );
        }),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe((logs) => {
        if (logs === null) {
          return;
        }
        const fetched = logs || [];
        this.hasNextPage = fetched.length > this.limit;
        // FE-027: precompute the details column once per fetch.
        this.logRows = fetched.slice(0, this.limit).map((log) => ({
          ...log,
          detailsText: this.formatDetails(log.details),
        }));
      });

    this.recomputeActiveFilters();
    this.loadTrigger$.next({ mode: 'immediate', resetPage: false });
  }

  /** FE-048: bound via ngModelChange on the two date inputs. */
  public onDateChange(): void {
    this.recomputeActiveFilters();
    this.loadTrigger$.next({ mode: 'debounced', resetPage: true });
  }

  private recomputeActiveFilters(): void {
    this.activeFiltersCount = (this.startDate ? 1 : 0) + (this.endDate ? 1 : 0);
  }

  private parseAndValidateDateRange(): { tstart?: number; tend?: number } | null {
    const tstart = this.startDate ? parseDatetimeLocalToMs(this.startDate) : undefined;
    const tend = this.endDate
      ? parseDatetimeLocalToMs(this.endDate, { endOfMinute: true })
      : undefined;

    if (tstart === null || tend === null) {
      return null;
    }

    // FE-048: a swapped range used to silently return an empty table — warn and
    // keep the current view instead.
    if (tstart !== undefined && tend !== undefined && tstart > tend) {
      this.notificationService.showError('Start date must be before the end date.');
      return null;
    }

    return { tstart, tend };
  }

  // --- Consolidated Actions ---

  /**
   * Called by the Refresh button. Resets pagination and applies current filters.
   */
  public refresh(): void {
    this.recomputeActiveFilters();
    if (!this.parseAndValidateDateRange()) {
      this.isLoading = false;
      this.cdr.markForCheck();
      return;
    }
    this.offset = 0;
    this.currentPage = 1;
    this.loadTrigger$.next({ mode: 'immediate', resetPage: false });
  }

  /**
   * Called by the Clear Filters button. Clears inputs and fetches again.
   */
  public clearFilters(): void {
    this.startDate = '';
    this.endDate = '';
    this.recomputeActiveFilters();
    this.offset = 0;
    this.currentPage = 1;
    this.loadTrigger$.next({ mode: 'immediate', resetPage: false });
  }

  // --- Pagination Methods ---

  public previousPage(): void {
    if (this.offset >= this.limit) {
      if (!this.parseAndValidateDateRange()) {
        this.isLoading = false;
        this.cdr.markForCheck();
        return;
      }
      this.offset -= this.limit;
      this.currentPage--;
      this.loadTrigger$.next({ mode: 'immediate', resetPage: false });
    }
  }

  public nextPage(): void {
    if (this.hasNextPage) {
      if (!this.parseAndValidateDateRange()) {
        this.isLoading = false;
        this.cdr.markForCheck();
        return;
      }
      this.offset += this.limit;
      this.currentPage++;
      this.loadTrigger$.next({ mode: 'immediate', resetPage: false });
    }
  }

  /**
   * Helper to format the dynamic 'details' JSON object into a readable string.
   * FE-027: called once per row per fetch (was called from the template per CD cycle).
   */
  public formatDetails(details: Record<string, unknown> | null | undefined): string {
    if (!details || Object.keys(details).length === 0) {
      return '-';
    }

    const formatted = Object.entries(details)
      .map(([key, value]) => {
        let formattedValue: string;
        if (value !== null && typeof value === 'object') {
          try {
            formattedValue = JSON.stringify(value);
          } catch {
            formattedValue = '[Unserializable]';
          }
        } else {
          formattedValue = String(value);
        }
        return `${key}: ${formattedValue}`;
      })
      .join(', ');

    const maxDetailsLength = 300;
    if (formatted.length > maxDetailsLength) {
      return `${formatted.slice(0, maxDetailsLength - 3)}...`;
    }
    return formatted;
  }

  trackByLogId(index: number, log: AuditLogRow): number {
    return log.id;
  }

  ngOnDestroy(): void {
    this.loadTrigger$.complete();
  }
}
