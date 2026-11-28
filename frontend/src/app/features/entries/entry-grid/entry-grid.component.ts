import {
  Component,
  Input,
  Output,
  EventEmitter,
  ChangeDetectionStrategy,
  OnChanges,
  OnInit,
  AfterViewInit,
  OnDestroy,
  SimpleChanges,
  ChangeDetectorRef,
  DestroyRef,
  ElementRef,
  NgZone,
  inject,
} from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { Entry } from '../../../core/models';
import { EntryService } from '../../../core/services/entry.service';
import { CommonModule } from '@angular/common';
import { EntryThumbnailComponent } from '../entry-thumbnail/entry-thumbnail.component';
import { fromEvent, Subject, Subscription } from 'rxjs';
import { debounceTime } from 'rxjs/operators';

export interface DateGroup {
  dateStr: string;
  entries: Entry[];
  // FE-027: precomputed layout metrics. The template used to call
  // isLargeGroup() / getGroupAspectRatio() per group per CD cycle.
  aspectRatio: number;
  isLarge: boolean;
}

@Component({
  selector: 'app-entry-grid',
  templateUrl: './entry-grid.component.html',
  styleUrls: ['./entry-grid.component.css'],
  standalone: true,
  imports: [CommonModule, EntryThumbnailComponent],
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class EntryGridComponent implements OnInit, OnChanges, AfterViewInit, OnDestroy {
  @Input() entries: Entry[] = [];
  @Input() dbId: string | null = null; // UPDATED: Changed from dbName to dbId

  // --- SELECTION INPUTS ---
  @Input() selectedIds = new Set<number>();

  @Output() entryClicked = new EventEmitter<Entry>();
  @Output() toggleSelection = new EventEmitter<{ entry: Entry; event: MouseEvent }>();

  public failedImageIds = new Set<number>();
  public dateGroups: DateGroup[] = [];
  public aspectRatios = new Map<number, number>();
  private previewUrls = new Map<number, string>();
  private _maxRowAspectRatio = 8;

  private resizeObserver: ResizeObserver | null = null;
  private resizeEvents$ = new Subject<void>();
  private resizeSub: Subscription | null = null;
  private readonly destroyRef = inject(DestroyRef);

  constructor(
    private entryService: EntryService,
    private cdr: ChangeDetectorRef,
    private el: ElementRef,
    private ngZone: NgZone,
  ) {}

  ngOnInit(): void {
    this.updateContainerWidth();
    // FE-027: ngOnChanges (which computes the group metrics) runs before the real
    // container width is known — recompute with the actual wrap threshold so the
    // first paint already renders large/small groups correctly (no resize needed).
    this.recalculateGroupAspectRatios();
  }

  ngAfterViewInit(): void {
    this.ngZone.runOutsideAngular(() => {
      if (typeof ResizeObserver !== 'undefined' && this.el?.nativeElement) {
        this.resizeEvents$
          .pipe(debounceTime(50), takeUntilDestroyed(this.destroyRef))
          .subscribe(() => {
            this.handleResize();
          });
        this.resizeObserver = new ResizeObserver(() => {
          this.resizeEvents$.next();
        });
        this.resizeObserver.observe(this.el.nativeElement);
      } else if (typeof window !== 'undefined') {
        this.resizeSub = fromEvent(window, 'resize')
          .pipe(debounceTime(50), takeUntilDestroyed(this.destroyRef))
          .subscribe(() => {
            this.handleResize();
          });
      }
    });

    this.updateContainerWidth();
    // FE-027: the element is in the DOM now — the real width (and therefore the
    // real wrap threshold) may differ from the ngOnInit guess.
    this.recalculateGroupAspectRatios();
    this.cdr.markForCheck();
  }

  ngOnChanges(changes: SimpleChanges): void {
    // Check for dbId changes
    if (changes['dbId'] || changes['entries']) {
      if (changes['dbId']) {
        this.failedImageIds.clear();
        this.aspectRatios.clear();
      } else if (changes['entries']) {
        // FE-031: prune the caches to the ids still on screen — aspectRatios and
        // failedImageIds used to grow without bound across list updates.
        const visibleIds = new Set(this.entries.map((e) => e.id));
        for (const id of Array.from(this.aspectRatios.keys())) {
          if (!visibleIds.has(id)) {
            this.aspectRatios.delete(id);
          }
        }
        for (const id of Array.from(this.failedImageIds)) {
          if (!visibleIds.has(id)) {
            this.failedImageIds.delete(id);
          }
        }
      }
      this.previewUrls.clear();
      if (this.dbId && this.entries) {
        for (const entry of this.entries) {
          this.previewUrls.set(entry.id, this.entryService.getEntryPreviewUrl(this.dbId, entry.id));
        }
      }
      this.groupEntries();
      this.recalculateGroupAspectRatios();
    }
  }

  private groupEntries(): void {
    if (!this.entries || this.entries.length === 0) {
      this.dateGroups = [];
      return;
    }

    const groupsMap = new Map<string, Entry[]>();
    const weekdays = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
    const months = [
      'Jan',
      'Feb',
      'Mar',
      'Apr',
      'May',
      'Jun',
      'Jul',
      'Aug',
      'Sep',
      'Oct',
      'Nov',
      'Dec',
    ];

    for (const entry of this.entries) {
      const date = new Date(entry.timestamp);

      const dayName = weekdays[date.getDay()];
      const dayVal = date.getDate();
      const monthName = months[date.getMonth()];
      const yearVal = date.getFullYear();
      const dateStr = `${dayName} ${dayVal} ${monthName} ${yearVal}`;

      if (!groupsMap.has(dateStr)) {
        groupsMap.set(dateStr, []);
      }
      groupsMap.get(dateStr)!.push(entry);
    }

    this.dateGroups = Array.from(groupsMap.entries()).map(([dateStr, entries]) => ({
      dateStr,
      entries,
      // FE-027: filled in by recalculateGroupAspectRatios() below.
      aspectRatio: 1.0,
      isLarge: false,
    }));
  }

  // FE-027: precompute the per-group layout metrics (sum of entry aspect ratios and
  // whether the group wraps). Recomputed when entries change, when an image reports
  // its real ratio and when the container is resized.
  private recalculateGroupAspectRatio(group: DateGroup): void {
    let sum = 0;
    for (const entry of group.entries) {
      sum += this.getAspectRatio(entry);
    }
    group.aspectRatio = sum > 0 ? sum : 1.0;
    group.isLarge = group.aspectRatio > this._maxRowAspectRatio;
  }

  private recalculateGroupAspectRatios(): void {
    for (const group of this.dateGroups) {
      this.recalculateGroupAspectRatio(group);
    }
  }

  public getPreviewUrl(entry: Entry): string {
    if (!this.dbId) return '';
    const cached = this.previewUrls.get(entry.id);
    if (cached !== undefined) {
      return cached;
    }
    const url = this.entryService.getEntryPreviewUrl(this.dbId, entry.id);
    this.previewUrls.set(entry.id, url);
    return url;
  }

  public onEntryClick(entry: Entry, event?: MouseEvent): void {
    // As soon as one image is selected, simply tapping other images selects those entries as well
    if (this.selectedIds.size > 0) {
      const mouseEvent = event || new MouseEvent('click', { bubbles: true, cancelable: true });
      this.toggleSelection.emit({ entry, event: mouseEvent });
      return;
    }

    this.entryClicked.emit(entry);
  }

  public onCheckboxClick(entry: Entry, event: MouseEvent): void {
    event.stopPropagation();
    this.toggleSelection.emit({ entry, event });
  }

  public onImageError(entryId: number): void {
    this.failedImageIds.add(entryId);
  }

  public trackById(index: number, entry: Entry): number {
    return entry.id;
  }

  public trackByDateStr(index: number, group: DateGroup): string {
    return group.dateStr;
  }

  public isSelected(entry: Entry): boolean {
    return this.selectedIds.has(entry.id);
  }

  public getEntryTitle(entry: Entry): string {
    return entry.filename || `ID: ${entry.id}`;
  }

  public getAspectRatio(entry: Entry): number {
    // 1. Check if we have dynamically loaded aspect ratio
    if (this.aspectRatios.has(entry.id)) {
      return this.aspectRatios.get(entry.id)!;
    }
    // 2. Check if we have width and height in metadata
    if (entry.media_fields?.width && entry.media_fields?.height) {
      const ar = entry.media_fields.width / entry.media_fields.height;
      return this.clampAspectRatio(ar);
    }
    // 3. Fallback to square
    return 1.0;
  }

  private clampAspectRatio(ar: number): number {
    if (isNaN(ar) || ar <= 0) return 1.0;
    if (ar > 2.5) return 2.5;
    if (ar < 0.4) return 0.4;
    return ar;
  }

  public onAspectLoaded(entryId: number, ar: number): void {
    const clamped = this.clampAspectRatio(ar);
    let owningGroup: DateGroup | undefined;
    let owningEntry: Entry | undefined;
    for (const group of this.dateGroups) {
      const found = group.entries.find((e) => e.id === entryId);
      if (found) {
        owningGroup = group;
        owningEntry = found;
        break;
      }
    }

    const current = owningEntry ? this.getAspectRatio(owningEntry) : this.aspectRatios.get(entryId);
    if (current !== undefined && Math.abs(current - clamped) <= 0.001) {
      return;
    }

    this.aspectRatios.set(entryId, clamped);
    if (owningGroup) {
      this.recalculateGroupAspectRatio(owningGroup);
    }
    this.cdr.markForCheck();
  }

  private handleResize(): void {
    const prev = this._maxRowAspectRatio;
    this.updateContainerWidth();
    if (Math.abs(prev - this._maxRowAspectRatio) > 0.001) {
      // FE-027: the wrap threshold changed with the width — refresh the group metrics.
      this.recalculateGroupAspectRatios();
      this.ngZone.run(() => {
        this.cdr.markForCheck();
      });
    }
  }

  private updateContainerWidth(): void {
    if (typeof window === 'undefined') {
      this._maxRowAspectRatio = 8;
      return;
    }
    let width = 0;
    if (this.el && this.el.nativeElement) {
      width = this.el.nativeElement.getBoundingClientRect().width;
    }
    if (width <= 0) {
      const isSidebarShown =
        window.location.pathname === '/dashboard' || window.location.pathname === '/';
      const sidebarWidth = isSidebarShown ? 260 : 0;
      const padding = 48;
      width = window.innerWidth - sidebarWidth - padding;
    }
    const tileHeight = window.innerWidth <= 768 ? 100 : 150;
    this._maxRowAspectRatio = width > 0 ? width / tileHeight : 8;
  }

  ngOnDestroy(): void {
    this.resizeEvents$.complete();
    if (this.resizeObserver) {
      this.resizeObserver.disconnect();
      this.resizeObserver = null;
    }
    if (this.resizeSub) {
      this.resizeSub.unsubscribe();
      this.resizeSub = null;
    }
  }
}
