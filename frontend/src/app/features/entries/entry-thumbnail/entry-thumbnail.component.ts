import {
  ChangeDetectionStrategy,
  Component,
  EventEmitter,
  HostBinding,
  Input,
  Output,
} from '@angular/core';
import { CommonModule } from '@angular/common';
import { SecureImageDirective } from '../../../shared/directives/secure-image.directive';
import { Entry } from '../../../core/models/entry.model';
import { EntryStatus } from '../../../core/models/enums';

/**
 * Renders the 4-state entry thumbnail preview (queued, processing, error, or ready/fallback image)
 * in either 'grid' (full-bleed tile) or 'list' (50x50 rounded preview) variant.
 */
@Component({
  selector: 'app-entry-thumbnail',
  standalone: true,
  imports: [CommonModule, SecureImageDirective],
  templateUrl: './entry-thumbnail.component.html',
  styleUrls: ['./entry-thumbnail.component.css'],
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class EntryThumbnailComponent {
  @Input() entry!: Entry;
  @Input() previewUrl = '';
  @Input() variant: 'grid' | 'list' = 'grid';
  @Input() failed = false;

  @Output() imageError = new EventEmitter<void>();
  @Output() aspectLoaded = new EventEmitter<number>();
  @Output() thumbnailClick = new EventEmitter<void>();

  public readonly EntryStatus = EntryStatus;

  @HostBinding('class.variant-grid')
  get isGridVariant(): boolean {
    return this.variant === 'grid';
  }

  @HostBinding('class.variant-list')
  get isListVariant(): boolean {
    return this.variant === 'list';
  }

  onImageClick(): void {
    this.thumbnailClick.emit();
  }
}
