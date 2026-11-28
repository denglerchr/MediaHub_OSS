import { CommonModule } from '@angular/common';
import {
  ChangeDetectionStrategy,
  ChangeDetectorRef,
  Component,
  Input,
  forwardRef,
  inject,
} from '@angular/core';
import { ControlValueAccessor, NG_VALUE_ACCESSOR } from '@angular/forms';

@Component({
  selector: 'app-toggle-switch',
  standalone: true,
  imports: [CommonModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  providers: [
    {
      provide: NG_VALUE_ACCESSOR,
      useExisting: forwardRef(() => ToggleSwitchComponent),
      multi: true,
    },
  ],
  template: `
    <div class="bool-field-container">
      <label class="toggle-switch">
        <input
          type="checkbox"
          role="switch"
          [id]="inputId || null"
          [checked]="checked"
          [attr.aria-checked]="checked"
          [disabled]="disabled"
          (change)="onCheckboxChange($event)"
          (blur)="onTouched()"
        />
        <span class="toggle-slider"></span>
      </label>
      <span *ngIf="showLabel" class="bool-field-value">
        {{ checked ? trueLabel : falseLabel }}
      </span>
    </div>
  `,
  styles: [
    `
      :host {
        display: block;
      }
    `,
  ],
})
export class ToggleSwitchComponent implements ControlValueAccessor {
  @Input() inputId = '';
  @Input() trueLabel = 'True';
  @Input() falseLabel = 'False';
  @Input() showLabel = true;

  checked = false;
  disabled = false;

  private readonly cdr = inject(ChangeDetectorRef);
  private onChange: (value: boolean) => void = () => {};
  onTouched: () => void = () => {};

  writeValue(val: unknown): void {
    this.checked = !!val;
    this.cdr.markForCheck();
  }

  registerOnChange(fn: (value: boolean) => void): void {
    this.onChange = fn;
  }

  registerOnTouched(fn: () => void): void {
    this.onTouched = fn;
  }

  setDisabledState(isDisabled: boolean): void {
    this.disabled = isDisabled;
    this.cdr.markForCheck();
  }

  onCheckboxChange(event: Event): void {
    if (this.disabled) {
      return;
    }
    this.checked = (event.target as HTMLInputElement).checked;
    this.onChange(this.checked);
    this.onTouched();
  }
}
