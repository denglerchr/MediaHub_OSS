import { Component, OnDestroy, OnInit } from '@angular/core';
import { Subject } from 'rxjs';
import { takeUntil } from 'rxjs/operators';
import { ModalService, ModalEvent } from '../../services/modal.service';

// 1. Expand the interface to support custom button text and styles
export interface ConfirmationModalData {
  title?: string;
  message: string;
  confirmText?: string;
  cancelText?: string;
  confirmButtonClass?: string;
}

@Component({
  selector: 'app-confirmation-modal',
  templateUrl: './confirmation-modal.component.html',
  styleUrls: ['./confirmation-modal.component.css'],
  standalone: false,
})
export class ConfirmationModalComponent implements OnInit, OnDestroy {
  public static readonly MODAL_ID = 'confirmationModal';

  // 2. Provide sensible defaults
  modalData: ConfirmationModalData = {
    message: 'Are you sure?',
    confirmText: 'Confirm',
    cancelText: 'Cancel',
    confirmButtonClass: 'btn-danger',
  };

  private destroy$ = new Subject<void>();

  constructor(private modalService: ModalService) {}

  ngOnInit(): void {
    this.modalService
      .getModalEvents(ConfirmationModalComponent.MODAL_ID)
      .pipe(takeUntil(this.destroy$))
      .subscribe((event: ModalEvent) => {
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

  ngOnDestroy(): void {
    this.destroy$.next();
    this.destroy$.complete();
  }
}
