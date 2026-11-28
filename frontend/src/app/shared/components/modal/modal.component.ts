import {
  Component,
  DestroyRef,
  Input,
  OnDestroy,
  OnInit,
  Renderer2,
  ViewChild,
  ElementRef,
  ChangeDetectorRef,
  ChangeDetectionStrategy,
  inject,
} from '@angular/core';
import { CommonModule } from '@angular/common';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { ModalService, ModalEvent } from '../../../core/services/modal.service';

// N-D4: a single key event must be handled by at most one modal even though every
// open ModalComponent listens on `document`: an Escape that closes the topmost
// modal would otherwise also be seen by the modal below it *after* the stacking
// order already changed (the next-in-line would close immediately, too).
const handledKeyEvents = new WeakSet<Event>();

@Component({
  selector: 'app-modal',
  templateUrl: './modal.component.html',
  styleUrls: ['./modal.component.css'],
  standalone: true,
  imports: [CommonModule],
  changeDetection: ChangeDetectionStrategy.OnPush, // Optimization for better performance
})
export class ModalComponent implements OnInit, OnDestroy {
  @Input() modalId: string = '';
  @Input() modalTitle: string = 'Modal';
  @Input() size: 'sm' | 'md' | 'lg' = 'md';

  // FE-044: the dialog element (focus target + focus-trap container).
  @ViewChild('modalContent') modalContent?: ElementRef<HTMLDivElement>;

  public isOpen = false;
  public overlayZIndex: number | null = null;
  public contentZIndex: number | null = null;
  // FE-044: the element focus returns to when the modal closes.
  private previouslyFocused: HTMLElement | null = null;
  // FE-044/FE-043: pending initial-focus task (tracked and cleared on destroy).
  private focusTimer?: ReturnType<typeof setTimeout>;
  private unlistenKeydown: (() => void) | null = null;
  private destroyRef = inject(DestroyRef);

  constructor(
    private modalService: ModalService,
    private cdr: ChangeDetectorRef,
    private renderer: Renderer2,
  ) {}

  ngOnInit(): void {
    this.modalService
      .getModalEvents(this.modalId)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe((event: ModalEvent) => {
        if (event.action === 'open') {
          // Capture the trigger only on initial open so a re-open preserves the original element.
          if (!this.isOpen) {
            this.previouslyFocused =
              document.activeElement instanceof HTMLElement ? document.activeElement : null;
          }
          this.isOpen = true;
          this.attachKeydownListener();
          document.body.classList.add('modal-open'); // Prevent background scrolling
          const layerIndex = Math.max(0, this.modalService.getLayerIndex(this.modalId));
          this.overlayZIndex = this.readZIndexVar('--z-modal-bg', 300) + layerIndex * 10;
          this.contentZIndex = this.readZIndexVar('--z-modal', 400) + layerIndex * 10;
          // FE-044: move focus into the dialog once it is rendered (the tracked timeout
          // fires after the current CD pass; the dialog lives behind an *ngIf and is not
          // in the DOM yet).
          this.cdr.markForCheck();
          if (this.focusTimer) {
            clearTimeout(this.focusTimer);
          }
          this.focusTimer = setTimeout(() => this.modalContent?.nativeElement.focus());
          return;
        } else if (event.action === 'close') {
          this.isOpen = false;
          this.detachKeydownListener();
          this.overlayZIndex = null;
          this.contentZIndex = null;
          if (this.focusTimer) {
            clearTimeout(this.focusTimer);
            this.focusTimer = undefined;
          }
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
    ).filter((el) => {
      if (el instanceof HTMLInputElement && el.type === 'hidden') {
        return false;
      }
      if (el.closest('[hidden]')) {
        return false;
      }
      const style = getComputedStyle(el);
      return style.visibility !== 'hidden' && style.display !== 'none';
    });

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

  private attachKeydownListener(): void {
    if (this.unlistenKeydown) {
      return;
    }
    this.unlistenKeydown = this.renderer.listen('document', 'keydown', (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        this.onEscKeydown(event);
      } else if (event.key === 'Tab') {
        this.onTabKeydown(event);
      }
    });
  }

  private detachKeydownListener(): void {
    if (this.unlistenKeydown) {
      this.unlistenKeydown();
      this.unlistenKeydown = null;
    }
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

  private readZIndexVar(name: string, fallback: number): number {
    const raw = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
    const parsed = parseInt(raw, 10);
    return Number.isFinite(parsed) ? parsed : fallback;
  }

  ngOnDestroy(): void {
    this.detachKeydownListener();
    if (this.focusTimer) {
      clearTimeout(this.focusTimer);
    }
    if (this.isOpen) {
      this.modalService.abandon(this.modalId);
    }
    if (!this.modalService.hasOpenModals()) {
      document.body.classList.remove('modal-open');
    }
  }
}
