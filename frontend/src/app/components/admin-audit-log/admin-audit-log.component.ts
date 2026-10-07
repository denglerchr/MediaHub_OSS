// frontend/src/app/components/admin-audit-log/admin-audit-log.component.ts

import { Component, OnInit, OnDestroy, ChangeDetectorRef } from '@angular/core';
import { Subject } from 'rxjs';
import { takeUntil, finalize, debounceTime } from 'rxjs/operators';
import { AuditService } from '../../services/audit.service';
import { NotificationService } from '../../services/notification.service';
import { AuditLog } from '../../models/audit.models';

/** FE-027: display row — `detailsText` is precomputed once per fetch, not per CD cycle. */
interface AuditLogRow extends AuditLog {
  detailsText: string;
}

@Component({
  selector: 'app-admin-audit-log',
  templateUrl: './admin-audit-log.component.html',
  styleUrls: ['./admin-audit-log.component.css'],
  standalone: false,
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

  // FE-048: auto-refresh trigger (debounced — the datetime inputs emit per keystroke).
  private dateChange$ = new Subject<void>();

  public toggleFilter(): void {
    this.isFilterCollapsed = !this.isFilterCollapsed;
  }

  private destroy$ = new Subject<void>();

  constructor(
    private auditService: AuditService,
    private notificationService: NotificationService,
    private cdr: ChangeDetectorRef,
  ) {}

  ngOnInit(): void {
    // FE-048: a changed date filter refreshes the table automatically instead of
    // waiting for the manual Refresh button.
    this.dateChange$
      .pipe(debounceTime(400), takeUntil(this.destroy$))
      .subscribe(() => this.refresh());

    this.fetchLogs();
  }

  /** FE-048: bound via ngModelChange on the two date inputs. */
  public onDateChange(): void {
    this.recomputeActiveFilters();
    this.dateChange$.next();
  }

  private recomputeActiveFilters(): void {
    this.activeFiltersCount = (this.startDate ? 1 : 0) + (this.endDate ? 1 : 0);
  }

  /**
   * Fetches the audit logs based on current pagination and filter states.
   */
  private fetchLogs(): void {
    this.recomputeActiveFilters();

    // new Date(string) parses the string as Local Time.
    // .getTime() converts that local time into the UTC epoch in milliseconds.
    const tstart = this.startDate ? new Date(this.startDate).getTime() : undefined;
    const tend = this.endDate ? new Date(this.endDate).getTime() : undefined;

    // FE-048: a swapped range used to silently return an empty table — warn and
    // keep the current view instead.
    if (tstart !== undefined && tend !== undefined && tstart > tend) {
      this.isLoading = false;
      this.notificationService.showError('Start date must be before the end date.');
      this.cdr.markForCheck();
      return;
    }

    this.isLoading = true;
    this.cdr.markForCheck();

    // FE-047: ask for one extra row — it is the "is there a next page?" signal.
    // (Fetching exactly `limit` made a total that is a multiple of the page size
    // show an empty trailing page.)
    this.auditService
      .getAuditLogs(this.limit + 1, this.offset, 'desc', tstart, tend)
      .pipe(
        takeUntil(this.destroy$),
        finalize(() => {
          this.isLoading = false;
          this.cdr.markForCheck();
        }),
      )
      .subscribe({
        next: (logs) => {
          const fetched = logs || [];
          this.hasNextPage = fetched.length > this.limit;
          // FE-027: precompute the details column once per fetch.
          this.logRows = fetched.slice(0, this.limit).map((log) => ({
            ...log,
            detailsText: this.formatDetails(log.details),
          }));
        },
        error: (err) => {
          console.error('Failed to load audit logs', err);
          this.logRows = [];
        },
      });
  }

  // --- Consolidated Actions ---

  /**
   * Called by the Refresh button. Resets pagination and applies current filters.
   */
  public refresh(): void {
    this.offset = 0;
    this.currentPage = 1;
    this.fetchLogs();
  }

  /**
   * Called by the Clear Filters button. Clears inputs and fetches again.
   */
  public clearFilters(): void {
    this.startDate = '';
    this.endDate = '';
    this.recomputeActiveFilters();
    this.refresh();
  }

  // --- Pagination Methods ---

  public previousPage(): void {
    if (this.offset >= this.limit) {
      this.offset -= this.limit;
      this.currentPage--;
      this.fetchLogs();
    }
  }

  public nextPage(): void {
    if (this.hasNextPage) {
      this.offset += this.limit;
      this.currentPage++;
      this.fetchLogs();
    }
  }

  /**
   * Helper to format the dynamic 'details' JSON object into a readable string.
   * FE-027: called once per row per fetch (was called from the template per CD cycle).
   */
  public formatDetails(details: Record<string, unknown>): string {
    if (!details || Object.keys(details).length === 0) {
      return '-';
    }

    return Object.entries(details)
      .map(([key, value]) => `${key}: ${value}`)
      .join(', ');
  }

  trackByLogId(index: number, log: AuditLogRow): number {
    return log.id;
  }

  ngOnDestroy(): void {
    this.destroy$.next();
    this.destroy$.complete();
    this.dateChange$.complete();
  }
}
