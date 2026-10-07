import { Component, OnDestroy, OnInit } from '@angular/core';
import { FormBuilder, FormGroup, Validators } from '@angular/forms';
import { Subject } from 'rxjs';
import { takeUntil } from 'rxjs/operators';
import { ModalService } from '../../services/modal.service';

export interface FullscreenSettings {
  delaySeconds: number;
  entryLimit: number;
  shuffle: boolean;
  repeat: boolean;
}

@Component({
  selector: 'app-fullscreen-settings-modal',
  templateUrl: './fullscreen-settings-modal.component.html',
  styleUrls: ['./fullscreen-settings-modal.component.css'], // Create an empty CSS file or omit if not needed
  standalone: false,
})
export class FullscreenSettingsModalComponent implements OnInit, OnDestroy {
  public static readonly MODAL_ID = 'fullscreenSettingsModal';

  public settingsForm: FormGroup;
  private destroy$ = new Subject<void>();

  constructor(
    private fb: FormBuilder,
    private modalService: ModalService,
  ) {
    // Initialize the form with the default values you requested
    this.settingsForm = this.fb.group({
      delaySeconds: [20, [Validators.required, Validators.min(1)]],
      entryLimit: [50, [Validators.required, Validators.min(1)]],
      shuffle: [false],
      repeat: [true], // Defaulting to true for typical slideshow behavior
    });
  }

  ngOnInit(): void {
    // Reset the form to defaults every time the modal is opened
    this.modalService
      .getModalEvents(FullscreenSettingsModalComponent.MODAL_ID)
      .pipe(takeUntil(this.destroy$))
      .subscribe((event) => {
        if (event.action === 'open') {
          this.settingsForm.reset({
            delaySeconds: 20,
            entryLimit: 50,
            shuffle: false,
            repeat: true,
          });
        }
      });
  }

  onSubmit(): void {
    if (this.settingsForm.invalid) {
      this.settingsForm.markAllAsTouched();
      return;
    }

    const settings: FullscreenSettings = this.settingsForm.value;

    // Close the modal and pass the configured settings back to the caller
    this.modalService.close(FullscreenSettingsModalComponent.MODAL_ID, settings);
  }

  closeModal(): void {
    // N7: the result contract is `FullscreenSettings | null` — `null` marks the
    // cancellation (it used to be `false`, which is not part of the type).
    this.modalService.close(FullscreenSettingsModalComponent.MODAL_ID, null);
  }

  ngOnDestroy(): void {
    this.destroy$.next();
    this.destroy$.complete();
  }
}
