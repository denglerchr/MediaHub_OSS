import { CommonModule } from '@angular/common';
import { Component, Input } from '@angular/core';
import { FormGroup, ReactiveFormsModule } from '@angular/forms';
import { CustomField } from '../../../core/models';
import { ToggleSwitchComponent } from '../../../shared/components/toggle-switch/toggle-switch.component';

@Component({
  selector: 'app-custom-fields-form',
  standalone: true,
  imports: [CommonModule, ReactiveFormsModule, ToggleSwitchComponent],
  templateUrl: './custom-fields-form.component.html',
  styles: [
    `
      :host {
        display: block;
      }

      .custom-fields-section {
        margin-top: 2rem;
        padding-top: 1.5rem;
        border-top: 1px solid var(--border-color);
      }

      .custom-fields-section h4 {
        margin-bottom: 1rem;
      }
    `,
  ],
})
export class CustomFieldsFormComponent {
  @Input() fields: CustomField[] = [];
  @Input() formGroup!: FormGroup;
  @Input() idPrefix = '';
  @Input() heading = 'Custom Metadata';

  trackByFieldId(_index: number, field: CustomField): number | string {
    return field.id ?? field.name;
  }
}
