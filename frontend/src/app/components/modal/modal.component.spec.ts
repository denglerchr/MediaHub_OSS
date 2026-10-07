// frontend/src/app/components/modal/modal.component.spec.ts
// Regression specs for FE-044 (modal accessibility: dialog roles on the dialog
// itself, initial focus into the dialog, focus restore to the trigger, Tab/
// Shift-Tab focus trap) and N-D4 (stacked modals: only the topmost open modal
// traps Tab/Escape and owns the body scroll lock).
import { Component } from '@angular/core';
import { ComponentFixture, TestBed, fakeAsync, tick } from '@angular/core/testing';
import { CommonModule } from '@angular/common';
import { ModalComponent } from './modal.component';
import { ModalService } from '../../services/modal.service';

@Component({
  // N-D4: the topmost modal is deliberately *first* in the DOM — handler
  // registration order must not decide which modal owns Tab/Escape. The stacked
  // test opens the bottom modal first so `topModal` is the topmost one.
  template: `
    <button id="trigger">Open</button>
    <app-modal [modalId]="topId" [modalTitle]="'Top Modal'">
      <button id="top-first">Top first</button>
      <button id="top-last">Top last</button>
    </app-modal>
    <app-modal [modalId]="bottomId" [modalTitle]="'Bottom Modal'">
      <button id="bottom-first">Bottom first</button>
      <button id="bottom-last">Bottom last</button>
    </app-modal>
  `,
  standalone: false,
})
class TestHostComponent {
  readonly topId = 'topModal';
  readonly bottomId = 'bottomModal';
}

describe('ModalComponent — FE-044 dialog accessibility / N-D4 stacked modals', () => {
  let fixture: ComponentFixture<TestHostComponent>;
  let modalService: ModalService;

  const dispatchKey = (key: string, shiftKey = false): void => {
    document.dispatchEvent(
      new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, shiftKey }),
    );
    fixture.detectChanges();
  };

  const dialogOf = (modalId: string): HTMLElement | null => {
    // Look the dialog up via its title element so the helper works on the pre- and
    // post-FE-044 markup alike (the role assertions must fail, not the lookup).
    const title = document.getElementById(`${modalId}-title`);
    return (title?.closest('.modal-content') as HTMLElement | null) ?? null;
  };

  const focusablesOf = (content: HTMLElement): HTMLElement[] => {
    return Array.from(
      content.querySelectorAll<HTMLElement>(
        'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])',
      ),
    );
  };

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      declarations: [ModalComponent, TestHostComponent],
      imports: [CommonModule],
    }).compileComponents();

    fixture = TestBed.createComponent(TestHostComponent);
    modalService = TestBed.inject(ModalService);
    fixture.detectChanges();
  });

  afterEach(() => {
    // The body class leaks across specs otherwise (defensive cleanup).
    document.body.classList.remove('modal-open');
  });

  it('FE-044: .modal-content carries role="dialog", aria-modal and aria-labelledby pointing at the title', fakeAsync(() => {
    modalService.open('bottomModal');
    fixture.detectChanges();
    tick();

    const content = dialogOf('bottomModal');
    expect(content).not.toBeNull();
    expect(content!.getAttribute('role')).toBe('dialog');
    expect(content!.getAttribute('aria-modal')).toBe('true');
    expect(content!.getAttribute('aria-labelledby')).toBe('bottomModal-title');
    expect(content!.getAttribute('tabindex')).toBe('-1');

    const title = content!.querySelector('#bottomModal-title');
    expect(title?.textContent).toBe('Bottom Modal');

    // The roles belong on the dialog, not on the backdrop.
    const overlay = fixture.nativeElement.querySelector('.modal-overlay');
    expect(overlay.getAttribute('role')).toBeNull();
    expect(overlay.getAttribute('aria-modal')).toBeNull();
  }));

  it('FE-044: opening moves focus into the dialog', fakeAsync(() => {
    modalService.open('bottomModal');
    fixture.detectChanges();
    tick();

    const content = dialogOf('bottomModal');
    expect(document.activeElement).toBe(content);
  }));

  it('FE-044: closing restores focus to the element that opened the modal', fakeAsync(() => {
    const trigger = fixture.nativeElement.querySelector('#trigger') as HTMLElement;
    trigger.focus();
    expect(document.activeElement).toBe(trigger);

    modalService.open('bottomModal');
    fixture.detectChanges();
    tick();
    expect(document.activeElement).toBe(dialogOf('bottomModal'));

    modalService.close('bottomModal');
    fixture.detectChanges();
    tick();

    expect(document.activeElement).toBe(trigger);
  }));

  it('FE-044: Tab wraps to the first and Shift-Tab wraps to the last focusable', fakeAsync(() => {
    modalService.open('bottomModal');
    fixture.detectChanges();
    tick();

    const content = dialogOf('bottomModal')!;
    const focusables = focusablesOf(content);
    expect(focusables.length).toBeGreaterThan(1);
    const first = focusables[0];
    const last = focusables[focusables.length - 1];

    // Tab from the last focusable wraps to the first.
    last.focus();
    dispatchKey('Tab');
    expect(document.activeElement).toBe(first);

    // Shift-Tab from the first wraps to the last.
    dispatchKey('Tab', true);
    expect(document.activeElement).toBe(last);
  }));

  it('FE-044: Tab pulls an escaped focus back into the dialog', fakeAsync(() => {
    modalService.open('bottomModal');
    fixture.detectChanges();
    tick();

    const content = dialogOf('bottomModal')!;
    const trigger = fixture.nativeElement.querySelector('#trigger') as HTMLElement;
    trigger.focus();
    expect(content.contains(document.activeElement)).toBeFalse();

    dispatchKey('Tab');
    expect(content.contains(document.activeElement)).toBeTrue();
  }));

  it('N-D4: Escape closes only the topmost of two stacked modals', fakeAsync(() => {
    modalService.open('bottomModal');
    modalService.open('topModal');
    fixture.detectChanges();
    tick();

    expect(dialogOf('bottomModal')).not.toBeNull();
    expect(dialogOf('topModal')).not.toBeNull();

    dispatchKey('Escape');

    // Only the topmost modal is dismissed; the one below stays open.
    expect(dialogOf('topModal')).toBeNull();
    expect(dialogOf('bottomModal')).not.toBeNull();

    dispatchKey('Escape');
    expect(dialogOf('bottomModal')).toBeNull();
  }));

  it('N-D4: Tab stays inside the topmost modal — the lower modal must not steal focus', fakeAsync(() => {
    modalService.open('bottomModal');
    modalService.open('topModal');
    fixture.detectChanges();
    tick();

    const topContent = dialogOf('topModal')!;
    const bottomContent = dialogOf('bottomModal')!;
    const topFocusables = focusablesOf(topContent);
    const topFirst = topFocusables[0];
    const topLast = topFocusables[topFocusables.length - 1];

    // Tab from the topmost dialog's last focusable wraps within the topmost dialog.
    topLast.focus();
    dispatchKey('Tab');
    expect(document.activeElement).toBe(topFirst);
    expect(bottomContent.contains(document.activeElement)).toBeFalse();

    dispatchKey('Tab', true);
    expect(document.activeElement).toBe(topLast);
  }));

  it('N-D4: the body scroll lock is released only when the last open modal closes', fakeAsync(() => {
    modalService.open('bottomModal');
    modalService.open('topModal');
    fixture.detectChanges();
    tick();
    expect(document.body.classList.contains('modal-open')).toBeTrue();

    modalService.close('topModal');
    fixture.detectChanges();
    tick();
    expect(document.body.classList.contains('modal-open')).toBeTrue();

    modalService.close('bottomModal');
    fixture.detectChanges();
    tick();
    expect(document.body.classList.contains('modal-open')).toBeFalse();
  }));
});
