// frontend/src/app/directives/datetime-default.directive.ts
import { Directive, HostListener, Input, Optional, Self } from '@angular/core';
import { NgControl } from '@angular/forms';

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
  @HostListener('focus')
  onInteraction(): void {
    // Only proceed if the control exists and is currently empty
    if (this.ngControl && this.ngControl.control && !this.ngControl.value) {
      const now = new Date();
      const year = now.getFullYear();

      // Pad single digits with leading zeros
      const month = String(now.getMonth() + 1).padStart(2, '0');
      const day = String(now.getDate()).padStart(2, '0');

      const time = this.defaultTime === 'end' ? '23:59' : '00:00';
      const defaultDateTime = `${year}-${month}-${day}T${time}`;

      // Update the form control value programmatically
      this.ngControl.control.setValue(defaultDateTime);
    }
  }
}
