// frontend/src/app/services/modal.service.spec.ts
// Regression specs for FE-019 (id-anchored generic ModalService: close targeting,
// orphaned-subject handling on re-open, and the requestClose interception hook).
import { ModalService, ModalEvent } from './modal.service';

describe('ModalService — FE-019 regressions', () => {
  let service: ModalService;

  beforeEach(() => {
    service = new ModalService();
  });

  it('FE-019: close targets the modal id it is given, not "the last one opened"', () => {
    const aResults: Array<boolean | null> = [];
    const bResults: Array<boolean | null> = [];

    service.open<boolean>('modalA').subscribe((r) => aResults.push(r));
    service.open<boolean>('modalB').subscribe((r) => bResults.push(r));

    service.close('modalA', true);

    expect(aResults).toEqual([true]);
    expect(bResults).toEqual([]); // B must be untouched

    service.close('modalB', false);
    expect(bResults).toEqual([false]);
  });

  it('FE-019: re-opening a live modal id cancels and completes the previous result stream', () => {
    const first: Array<boolean | null> = [];
    let firstCompleted = false;

    service.open<boolean>('modalA').subscribe({
      next: (r) => first.push(r),
      complete: () => (firstCompleted = true),
    });

    const second: Array<boolean | null> = [];
    service.open<boolean>('modalA').subscribe((r) => second.push(r));

    expect(first).toEqual([null]); // cancelled, not hung forever
    expect(firstCompleted).toBeTrue();

    service.close('modalA', true);
    expect(second).toEqual([true]);
  });

  it('FE-019: emitResult is id-anchored and does not close the modal', () => {
    const results: Array<boolean | null> = [];
    const events: ModalEvent[] = [];
    service.getModalEvents('modalA').subscribe((e) => events.push(e));
    service.open<boolean>('modalA').subscribe((r) => results.push(r));

    service.emitResult('modalA', true);

    expect(results).toEqual([true]);
    expect(events.map((e) => e.action)).toEqual(['open']); // no close event
  });

  it('FE-019: the close guard blocks dismissal (covers ESC/X/overlay, which funnel through close())', () => {
    const results: Array<boolean | null> = [];
    const events: ModalEvent[] = [];
    service.getModalEvents('modalA').subscribe((e) => events.push(e));
    service.open<boolean>('modalA').subscribe((r) => results.push(r));

    const guard = jasmine.createSpy('guard').and.returnValue(false);
    service.registerCloseGuard('modalA', guard);

    service.close('modalA', false);

    expect(guard).toHaveBeenCalled();
    expect(results).toEqual([]); // modal stays open
    expect(events.map((e) => e.action)).toEqual(['open']); // no close event

    guard.and.returnValue(true);
    service.close('modalA', false);
    expect(results).toEqual([false]);
    expect(events.map((e) => e.action)).toEqual(['open', 'close']);
  });

  it('FE-019: unregisterCloseGuard restores normal dismissal', () => {
    service.open<boolean>('modalA').subscribe();
    service.registerCloseGuard('modalA', () => false);
    service.unregisterCloseGuard('modalA');

    let closed = false;
    service.getModalEvents('modalA').subscribe((e) => (closed = closed || e.action === 'close'));
    service.close('modalA');
    expect(closed).toBeTrue();
  });

  it('FE-019: open<T> delivers typed results (no `as any` smuggling)', () => {
    interface Settings {
      delaySeconds: number;
    }
    const received: { settings: Settings | null } = { settings: null };
    service.open<Settings>('settingsModal').subscribe((r) => (received.settings = r));

    service.close<Settings>('settingsModal', { delaySeconds: 20 });
    expect(received.settings).toEqual({ delaySeconds: 20 });
  });
});
