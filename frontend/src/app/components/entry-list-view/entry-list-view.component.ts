import { Component, Input, Output, EventEmitter, ChangeDetectionStrategy, OnChanges, SimpleChanges } from '@angular/core';
import { Entry, User } from '../../models'; 
import { EntryService } from '../../services/entry.service';
import { CommonModule, DatePipe, DecimalPipe } from '@angular/common';
import { SecureImageDirective } from '../../directives/secure-image.directive';
import { FormatBytesPipe } from '../../pipes/format-bytes.pipe';

@Component({
  selector: 'app-entry-list-view',
  templateUrl: './entry-list-view.component.html',
  styleUrls: ['./entry-list-view.component.css'],
  standalone: true,
  imports: [
    CommonModule, 
    DatePipe,
    DecimalPipe,
    SecureImageDirective,
    FormatBytesPipe
  ], 
  changeDetection: ChangeDetectionStrategy.OnPush
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
  @Output() toggleSelection = new EventEmitter<{ entry: Entry, event: MouseEvent }>();

  public failedImageIds = new Set<number>();
  
  // Scoped permission flags
  public canEdit = false;
  public canDelete = false;

  constructor(
    private entryService: EntryService
  ) {}

  ngOnChanges(changes: SimpleChanges): void {
    // UPDATED: Check for dbId changes
    if (changes['dbId'] || changes['entries']) {
      this.failedImageIds.clear();
    }
    // Calculate permissions whenever the user or database context changes
    if (changes['user'] || changes['dbId']) {
      this.updatePermissions();
    }
  }

  // Resolves permissions specifically for the currently displayed database
  private updatePermissions(): void {
    if (!this.user || !this.dbId) { // UPDATED
      this.canEdit = false;
      this.canDelete = false;
      return;
    }

    if (this.user.is_admin) {
      this.canEdit = true;
      this.canDelete = true;
    } else {
      // UPDATED: Match the permission's database_id against our component's dbId input
      const dbPerm = this.user.permissions?.find(p => p.database_id === this.dbId);
      this.canEdit = dbPerm?.can_edit || false;
      this.canDelete = dbPerm?.can_delete || false;
    }
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

  public isCoordinate(val: any): boolean {
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

  public formatCoordinate(val: any): string {
    if (!this.isCoordinate(val)) return '';
    return `${val.latitude.toFixed(6)}, ${val.longitude.toFixed(6)}`;
  }

  public getOpenStreetMapUrl(val: any): string {
    if (!this.isCoordinate(val)) return '#';
    return `https://www.openstreetmap.org/?mlat=${val.latitude}&mlon=${val.longitude}#map=16/${val.latitude}/${val.longitude}`;
  }

  public trackById(index: number, entry: Entry): number {
    return entry.id;
  }

  public trackByColumn(index: number, colName: string): string {
    return colName;
  }
}