import { CommonModule } from '@angular/common';
import { Component, Input } from '@angular/core';
import { FormGroup, ReactiveFormsModule } from '@angular/forms';
import {
  DURATION_UNIT_OPTIONS,
  HousekeepingUnitOption,
  SIZE_UNIT_OPTIONS,
} from '../../../shared/utils/housekeeping';

@Component({
  selector: 'app-housekeeping-fields',
  standalone: true,
  imports: [CommonModule, ReactiveFormsModule],
  templateUrl: './housekeeping-fields.component.html',
  styles: [
    `
      :host {
        display: block;
      }

      .form-grid-three {
        display: grid;
        grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
        gap: 1rem;
      }

      .form-group input:read-only {
        background-color: var(--input-bg);
        opacity: 0.7;
        cursor: not-allowed;
      }

      .helper-icon {
        display: inline-block;
        width: 1rem;
        height: 1rem;
        border-radius: 50%;
        background-color: var(--text-muted);
        color: var(--app-bg);
        font-size: 0.75rem;
        font-weight: bold;
        text-align: center;
        line-height: 1rem;
        cursor: help;
        user-select: none;
      }
    `,
  ],
})
export class HousekeepingFieldsComponent {
  @Input() formGroup!: FormGroup;
  @Input() readOnly = false;
  @Input() showUnitSelect = true;
  @Input() showHelperIcons = false;
  @Input() parseWarning: string | null = null;

  readonly durationUnitOptions = DURATION_UNIT_OPTIONS;
  readonly sizeUnitOptions = SIZE_UNIT_OPTIONS;

  trackByValue(_index: number, option: HousekeepingUnitOption): string {
    return option.value;
  }
}
