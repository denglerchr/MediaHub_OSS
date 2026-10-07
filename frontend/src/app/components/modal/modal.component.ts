import {
  Component,
  Input,
  OnDestroy,
  OnInit,
  HostListener,
  ViewChild,
  ElementRef,
  ChangeDetectorRef,
  ChangeDetectionStrategy,
} from '@angular/core';
import { Subject } from 'rxjs';
import { takeUntil } from 'rxjs/operators';
import { ModalService, ModalEvent } from '../../services/modal.service';

// N-D4: a single key event must be handled by at most one modal even though every
// open ModalComponent listens on `document`: an Escape that closes the topmost
// modal would otherwise also be seen by the modal below it *after* the stacking
// order already changed (the next-in-line would close immediately, too).
const handledKeyEvents = new WeakSet<Event>();

@Component({
  selector: 'app-modal',
  templateUrl: './modal.component.html',
  styleUrls: ['./modal.component.css'],
  standalone: false,
  changeDetection: ChangeDetectionStrategy.OnPush, // Optimization for better performance
})
export class ModalComponent implements OnInit, OnDestroy {
  @Input() modalId: string = '';
  @Input() modalTitle: string = 'Modal';
  @Input() size: 'sm' | 'md' | 'lg' = 'md';

  // FE-044: the dialog element (focus target + focus-trap container).
  @ViewChild('modalContent') modalContent?: ElementRef<HTMLDivElement>;

  public isOpen = false;
  // FE-044: the element focus returns to when the modal closes.
  private previouslyFocused: HTMLElement | null = null;
  // FE-044/FE-043: pending initial-focus task (tracked and cleared on destroy).
  private focusTimer?: ReturnType<typeof setTimeout>;
  private destroy$ = new Subject<void>();

  constructor(
    private modalService: ModalService,
    private cdr: ChangeDetectorRef,
  ) {}

  ngOnInit(): void {
    this.modalService
      .getModalEvents(this.modalId)
      .pipe(takeUntil(this.destroy$))
      .subscribe((event: ModalEvent) => {
        if (event.action === 'open') {
          this.isOpen = true;
          document.body.classList.add('modal-open'); // Prevent background scrolling
          // FE-044: remember the trigger element and move focus into the dialog once
          // it is rendered (the tracked timeout fires after the current CD pass; the
          // dialog lives behind an *ngIf and is not in the DOM yet).
          this.previouslyFocused =
            document.activeElement instanceof HTMLElement ? document.activeElement : null;
          this.cdr.markForCheck();
          if (this.focusTimer) {
            clearTimeout(this.focusTimer);
          }
          this.focusTimer = setTimeout(() => this.modalContent?.nativeElement.focus());
          return;
        } else if (event.action === 'close') {
          this.isOpen = false;
          // N-D4: with stacked modals the scroll lock belongs to the stack — only
          // the last closing modal releases it.
          if (!this.modalService.hasOpenModals()) {
            document.body.classList.remove('modal-open'); // Restore background scrolling
          }
          this.restoreFocus();
        }

        // Inform Angular to update the view since we are using OnPush
        this.cdr.markForCheck();
      });
  }

  // UX Improvement: Close modal on 'Escape' key press
  // N-D4: only the topmost open modal reacts (and only once per event).
  @HostListener('document:keydown.escape', ['$event'])
  onEscKeydown(event: Event): void {
    if (
      !this.isOpen ||
      !this.modalService.isTopmostOpen(this.modalId) ||
      handledKeyEvents.has(event)
    ) {
      return;
    }
    handledKeyEvents.add(event);
    this.closeModal();
  }

  /**
   * FE-044: keep Tab focus inside the dialog while it is open (focus trap).
   * N-D4: only the topmost open modal traps (and only once per event).
   */
  @HostListener('document:keydown.tab', ['$event'])
  onTabKeydown(event: Event): void {
    if (
      !this.isOpen ||
      !this.modalService.isTopmostOpen(this.modalId) ||
      handledKeyEvents.has(event)
    ) {
      return;
    }
    handledKeyEvents.add(event);
    const keyEvent = event as KeyboardEvent;
    const content = this.modalContent?.nativeElement;
    if (!content) {
      return;
    }

    const focusables = Array.from(
      content.querySelectorAll<HTMLElement>(
        'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])',
      ),
    );

    if (focusables.length === 0) {
      keyEvent.preventDefault();
      content.focus();
      return;
    }

    const first = focusables[0];
    const last = focusables[focusables.length - 1];
    const active = document.activeElement;

    if (keyEvent.shiftKey) {
      if (active === first || active === content || !content.contains(active)) {
        keyEvent.preventDefault();
        last.focus();
      }
    } else if (active === last || !content.contains(active)) {
      keyEvent.preventDefault();
      first.focus();
    }
  }

  /**
   * FE-044: Angular's KeyEventsPlugin matches modifiers exactly (`shift.tab` !==
   * `tab`), so the Shift-Tab wrap needs its own binding — `document:keydown.tab`
   * alone never fires for Shift+Tab and focus could walk out backwards.
   */
  @HostListener('document:keydown.shift.tab', ['$event'])
  onShiftTabKeydown(event: Event): void {
    this.onTabKeydown(event);
  }

  closeModal(): void {
    // Calling the service will emit the 'close' event, updating the state cleanly
    this.modalService.close(this.modalId);
  }

  onContentClick(event: MouseEvent): void {
    // Prevent clicks inside the modal content area from bubbling up and closing the overlay
    event.stopPropagation();
  }

  /** FE-044: return focus to the element that opened the modal. */
  private restoreFocus(): void {
    const target = this.previouslyFocused;
    this.previouslyFocused = null;
    if (target && document.contains(target)) {
      target.focus();
    }
  }

  ngOnDestroy(): void {
    if (this.focusTimer) {
      clearTimeout(this.focusTimer);
    }
    this.destroy$.next();
    this.destroy$.complete();
    // N-D4: drop the stacking entry and release the scroll lock only when this was
    // the last open modal (safety cleanup for a modal destroyed while open).
    this.modalService.release(this.modalId);
    if (!this.modalService.hasOpenModals()) {
      document.body.classList.remove('modal-open');
    }
  }
}
