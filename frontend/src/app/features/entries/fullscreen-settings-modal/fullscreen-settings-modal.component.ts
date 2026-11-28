import { CommonModule } from '@angular/common';
import { Component, DestroyRef, OnInit, inject } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormBuilder, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { FullscreenSettings } from '../../../core/models';
import { ModalService } from '../../../core/services/modal.service';
import { ModalComponent } from '../../../shared/components/modal/modal.component';
import { ToggleSwitchComponent } from '../../../shared/components/toggle-switch/toggle-switch.component';

export type { FullscreenSettings };

@Component({
  selector: 'app-fullscreen-settings-modal',
  templateUrl: './fullscreen-settings-modal.component.html',
  styleUrls: ['./fullscreen-settings-modal.component.css'], // Create an empty CSS file or omit if not needed
  standalone: true,
  imports: [CommonModule, ReactiveFormsModule, ModalComponent, ToggleSwitchComponent],
})
export class FullscreenSettingsModalComponent implements OnInit {
  public static readonly MODAL_ID = 'fullscreenSettingsModal';

  public settingsForm: FormGroup;
  private readonly destroyRef = inject(DestroyRef);

  constructor(
    private fb: FormBuilder,
    private modalService: ModalService,
  ) {
    // Initialize the form with the default values you requested
    this.settingsForm = this.fb.group({
      delaySeconds: [20, [Validators.required, Validators.min(1)]],
      entryLimit: [
        50,
        [Validators.required, Validators.min(1), Validators.max(1000), Validators.pattern(/^\d+$/)],
      ],
      shuffle: [false],
      repeat: [true], // Defaulting to true for typical slideshow behavior
    });
  }

  ngOnInit(): void {
    // Reset the form to defaults every time the modal is opened
    this.modalService
      .getModalEvents(FullscreenSettingsModalComponent.MODAL_ID)
      .pipe(takeUntilDestroyed(this.destroyRef))
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
}
