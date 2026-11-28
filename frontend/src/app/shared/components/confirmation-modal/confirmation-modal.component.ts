import { Component, DestroyRef, OnInit, inject } from '@angular/core';
import { CommonModule } from '@angular/common';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { ConfirmationModalData } from '../../../core/models';
import { ModalService } from '../../../core/services/modal.service';
import { ModalComponent } from '../modal/modal.component';

export type { ConfirmationModalData };

@Component({
  selector: 'app-confirmation-modal',
  templateUrl: './confirmation-modal.component.html',
  styleUrls: ['./confirmation-modal.component.css'],
  standalone: true,
  imports: [CommonModule, ModalComponent],
})
export class ConfirmationModalComponent implements OnInit {
  public static readonly MODAL_ID = 'confirmationModal';

  // 2. Provide sensible defaults
  modalData: ConfirmationModalData = {
    message: 'Are you sure?',
    confirmText: 'Confirm',
    cancelText: 'Cancel',
    confirmButtonClass: 'btn-danger',
  };

  private destroyRef = inject(DestroyRef);

  constructor(private modalService: ModalService) {}

  ngOnInit(): void {
    this.modalService
      .getModalEvents(ConfirmationModalComponent.MODAL_ID)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe((event) => {
        if (event.action === 'open' && event.data) {
          // 3. Map the incoming data with fallbacks
          this.modalData = {
            title: event.data.title || 'Confirm Action',
            message: event.data.message || 'Are you sure?',
            confirmText: event.data.confirmText || 'Confirm',
            cancelText: event.data.cancelText || 'Cancel',
            confirmButtonClass: event.data.confirmButtonClass || 'btn-danger',
          };
        }
      });
  }

  onConfirm(): void {
    this.modalService.close(ConfirmationModalComponent.MODAL_ID, true); // Close with a `true` result
  }

  onCancel(): void {
    this.modalService.close(ConfirmationModalComponent.MODAL_ID, false); // Close with a `false` result
  }
}
