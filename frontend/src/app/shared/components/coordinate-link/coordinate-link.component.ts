import { ChangeDetectionStrategy, Component, Input, OnChanges, SimpleChanges } from '@angular/core';
import { CommonModule } from '@angular/common';
import {
  MAX_LAT,
  MAX_LNG,
  MIN_LAT,
  MIN_LNG,
  formatCoordinate,
  getOpenStreetMapUrl,
  isCoordinate,
} from '../../utils/coordinates';

/**
 * Renders a validated geographic coordinate as an external OpenStreetMap link,
 * or a configurable fallback text when the value is not a valid coordinate.
 */
@Component({
  selector: 'app-coordinate-link',
  standalone: true,
  imports: [CommonModule],
  template: `
    <a
      *ngIf="valid; else invalidTpl"
      [href]="osmUrl"
      target="_blank"
      rel="noopener noreferrer"
      [class.coordinate-detail-link]="variant === 'detail'"
      [class.coordinate-table-link]="variant === 'table'"
      (click)="onLinkClick($event)"
      [title]="variant === 'detail' ? 'Open location in OpenStreetMap' : 'View in OpenStreetMap'"
      >📍 {{ formatted }}<span *ngIf="variant === 'detail'" class="external-icon"> ↗</span></a
    >
    <ng-template #invalidTpl>
      <ng-container *ngIf="fallback">{{ fallback }}</ng-container>
    </ng-template>
  `,
  styles: [
    `
      .coordinate-detail-link {
        display: inline-flex;
        align-items: center;
        gap: 0.35rem;
        color: var(--text-accent);
        text-decoration: none;
        font-weight: 500;
        transition: color 0.15s ease-in-out;
      }

      .coordinate-detail-link:hover {
        text-decoration: underline;
        color: var(--text-accent-hover);
      }

      .coordinate-detail-link .external-icon {
        font-size: 0.8em;
        opacity: 0.75;
      }

      .coordinate-table-link {
        color: var(--text-accent);
        text-decoration: none;
        font-weight: 500;
        display: inline-flex;
        align-items: center;
        gap: 0.25rem;
        white-space: nowrap;
      }

      .coordinate-table-link:hover {
        text-decoration: underline;
        color: var(--text-accent-hover);
      }
    `,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class CoordinateLinkComponent implements OnChanges {
  @Input() value: unknown;
  @Input() variant: 'detail' | 'table' = 'detail';
  @Input() stopPropagation = false;
  @Input() fallback = 'N/A';

  valid = false;
  formatted = '';
  osmUrl = '#';

  ngOnChanges(changes: SimpleChanges): void {
    if (changes['value']) {
      this.updateCoordinateState();
    }
  }

  onLinkClick(event: Event): void {
    if (this.stopPropagation) {
      event.stopPropagation();
    }
  }

  private updateCoordinateState(): void {
    if (
      isCoordinate(this.value) &&
      this.value.latitude >= MIN_LAT &&
      this.value.latitude <= MAX_LAT &&
      this.value.longitude >= MIN_LNG &&
      this.value.longitude <= MAX_LNG
    ) {
      this.valid = true;
      this.formatted = formatCoordinate(this.value);
      this.osmUrl = getOpenStreetMapUrl(this.value);
    } else {
      this.valid = false;
      this.formatted = '';
      this.osmUrl = '#';
    }
  }
}
