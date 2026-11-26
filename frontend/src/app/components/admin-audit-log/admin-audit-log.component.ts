// frontend/src/app/components/admin-audit-log/admin-audit-log.component.ts

import { Component, OnInit, OnDestroy, ChangeDetectorRef } from '@angular/core';
import { Subject, of } from 'rxjs';
import { takeUntil, finalize, debounceTime, switchMap, catchError } from 'rxjs/operators';
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
  private fetchTrigger$ = new Subject<{ offset: number; tstart?: number; tend?: number }>();

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

    this.fetchTrigger$
      .pipe(
        switchMap(({ offset, tstart, tend }) => {
          this.isLoading = true;
          this.cdr.markForCheck();

          // FE-047: ask for one extra row — it is the "is there a next page?" signal.
          // (Fetching exactly `limit` made a total that is a multiple of the page size
          // show an empty trailing page.)
          return this.auditService.getAuditLogs(this.limit + 1, offset, 'desc', tstart, tend).pipe(
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
        takeUntil(this.destroy$),
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
    let tend = this.endDate ? new Date(this.endDate).getTime() : undefined;

    if (
      (tstart !== undefined && Number.isNaN(tstart)) ||
      (tend !== undefined && Number.isNaN(tend))
    ) {
      this.isLoading = false;
      this.cdr.markForCheck();
      return;
    }

    if (tend !== undefined && /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(this.endDate)) {
      tend += 59999;
    }

    // FE-048: a swapped range used to silently return an empty table — warn and
    // keep the current view instead.
    if (tstart !== undefined && tend !== undefined && tstart > tend) {
      this.isLoading = false;
      this.notificationService.showError('Start date must be before the end date.');
      this.cdr.markForCheck();
      return;
    }

    this.fetchTrigger$.next({ offset: this.offset, tstart, tend });
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
    this.destroy$.next();
    this.destroy$.complete();
    this.dateChange$.complete();
    this.fetchTrigger$.complete();
  }
}
