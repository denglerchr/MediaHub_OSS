import {
  Component,
  Input,
  Output,
  EventEmitter,
  ChangeDetectionStrategy,
  OnChanges,
  SimpleChanges,
} from '@angular/core';
import { Entry, User } from '../../../core/models';
import { EntryStatus } from '../../../core/models/enums';
import { EntryService } from '../../../core/services/entry.service';
import { CommonModule, DatePipe, DecimalPipe } from '@angular/common';
import { EntryThumbnailComponent } from '../entry-thumbnail/entry-thumbnail.component';
import { CoordinateLinkComponent } from '../../../shared/components/coordinate-link/coordinate-link.component';
import { FormatBytesPipe } from '../../../shared/pipes/format-bytes.pipe';
import { isCoordinate } from '../../../shared/utils/coordinates';
import { canDeleteEntry, canEditEntry, isEntryQueued } from '../../../shared/utils/entry-domain';
import { resolveDatabasePermissions } from '../../../shared/utils/permissions';

@Component({
  selector: 'app-entry-list-view',
  templateUrl: './entry-list-view.component.html',
  styleUrls: ['./entry-list-view.component.css'],
  standalone: true,
  imports: [
    CommonModule,
    DatePipe,
    DecimalPipe,
    EntryThumbnailComponent,
    CoordinateLinkComponent,
    FormatBytesPipe,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class EntryListViewComponent implements OnChanges {
  @Input() entries: Entry[] = [];
  @Input() tableColumns: string[] = [];
  @Input() user: User | null = null;
  @Input() dbId: string | null = null; // UPDATED: Changed from dbName to dbId
  @Input() selectedIds = new Set<number>();

  @Output() entryClicked = new EventEmitter<Entry>();
  @Output() editClicked = new EventEmitter<Entry>();
  @Output() deleteClicked = new EventEmitter<Entry>();
  @Output() toggleSelection = new EventEmitter<{ entry: Entry; event: MouseEvent }>();

  public readonly EntryStatus = EntryStatus;
  public readonly canEditEntry = canEditEntry;
  public readonly canDeleteEntry = canDeleteEntry;
  public readonly isEntryQueued = isEntryQueued;
  public readonly isCoordinate = isCoordinate;

  public failedImageIds = new Set<number>();

  // Scoped permission flags
  public canEdit = false;
  public canDelete = false;

  constructor(private entryService: EntryService) {}

  ngOnChanges(changes: SimpleChanges): void {
    // Prune failed preview cache on entries refresh; clear completely only when dbId changes.
    if (changes['dbId']) {
      this.failedImageIds.clear();
    } else if (changes['entries']) {
      const visibleIds = new Set(this.entries.map((e) => e.id));
      for (const id of Array.from(this.failedImageIds)) {
        if (!visibleIds.has(id)) {
          this.failedImageIds.delete(id);
        }
      }
    }
    // Calculate permissions whenever the user or database context changes
    if (changes['user'] || changes['dbId']) {
      this.updatePermissions();
    }
  }

  // Resolves permissions specifically for the currently displayed database
  private updatePermissions(): void {
    const perms = resolveDatabasePermissions(this.user, this.dbId);
    this.canEdit = perms.canEdit;
    this.canDelete = perms.canDelete;
  }

  public getPreviewUrl(entry: Entry): string {
    if (!this.dbId) return '';
    return this.entryService.getEntryPreviewUrl(this.dbId, entry.id); // UPDATED: Pass dbId
  }

  public onEntryClick(entry: Entry): void {
    this.entryClicked.emit(entry);
  }

  public onEditClick(entry: Entry): void {
    this.editClicked.emit(entry);
  }

  public onDeleteClick(entry: Entry): void {
    this.deleteClicked.emit(entry);
  }

  public onCheckboxClick(entry: Entry, event: MouseEvent): void {
    event.preventDefault();
    event.stopPropagation();

    if (event.target instanceof HTMLElement) {
      event.target.blur();
    }

    this.toggleSelection.emit({ entry, event });
  }

  public isSelected(entry: Entry): boolean {
    return this.selectedIds.has(entry.id);
  }

  public onImageError(entryId: number): void {
    this.failedImageIds.add(entryId);
  }

  public trackById(index: number, entry: Entry): number {
    return entry.id;
  }

  public trackByColumn(index: number, colName: string): string {
    return colName;
  }
}
