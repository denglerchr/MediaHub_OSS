// frontend/src/app/directives/datetime-default.directive.ts
import { Directive, HostListener, Input, Optional, Self } from '@angular/core';
import { NgControl } from '@angular/forms';
import { toDatetimeLocal } from '../utils/datetime';

@Directive({
  selector: '[appDatetimeDefault]',
  standalone: true,
})
export class DatetimeDefaultDirective {
  /**
   * FE-048: which end of the day the auto-filled default snaps to. "start" fills
   * T00:00 (as before); "end" fills T23:59 — an *end-of-range* input defaulting to
   * T00:00 silently excluded the entire selected day from the filter.
   * Usage: `appDatetimeDefault` (= "start") or `appDatetimeDefault="end"`.
   * (`''` is the bare-attribute form.)
   */
  @Input('appDatetimeDefault') defaultTime: 'start' | 'end' | '' = 'start';

  // Inject NgControl to cleanly update Reactive Forms
  constructor(@Optional() @Self() private ngControl: NgControl) {}

  // Automatically listen to click and focus events on the host element
  @HostListener('click')
  onClick(): void {
    this.applyDefaultIfEmpty();
  }

  @HostListener('focus')
  onFocus(): void {
    this.applyDefaultIfEmpty();
  }

  private applyDefaultIfEmpty(): void {
    // Only proceed if the control exists, is enabled, and is currently empty
    if (
      this.ngControl &&
      this.ngControl.control &&
      !this.ngControl.disabled &&
      (this.ngControl.value == null || this.ngControl.value === '')
    ) {
      const target = new Date();
      if (this.defaultTime === 'end') {
        target.setHours(23, 59, 0, 0);
      } else {
        target.setHours(0, 0, 0, 0);
      }

      // Update the form control value programmatically
      this.ngControl.control.setValue(toDatetimeLocal(target, 'minutes'));
    }
  }
}
