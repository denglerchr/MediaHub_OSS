// frontend/src/app/services/modal.service.spec.ts
// Regression specs for FE-019 (id-anchored generic ModalService: close targeting,
// orphaned-subject handling on re-open, and the requestClose interception hook).
import { ModalService, ModalEvent } from './modal.service';
import { ConfirmationModalData, FullscreenSettings } from '../models';

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

  it('tracks open modal stacking order via getStackIndex', () => {
    expect(service.getStackIndex('modalA')).toBe(-1);

    service.open<boolean>('modalA');
    expect(service.getStackIndex('modalA')).toBe(0);

    service.open<boolean>('modalB');
    expect(service.getStackIndex('modalA')).toBe(0);
    expect(service.getStackIndex('modalB')).toBe(1);

    // Re-opening modalA moves it to the top of the stack
    service.open<boolean>('modalA');
    expect(service.getStackIndex('modalB')).toBe(0);
    expect(service.getStackIndex('modalA')).toBe(1);

    service.close('modalA');
    expect(service.getStackIndex('modalA')).toBe(-1);
    expect(service.getStackIndex('modalB')).toBe(0);

    service.close('modalB');
    expect(service.getStackIndex('modalB')).toBe(-1);
  });

  describe('confirm()', () => {
    const data: ConfirmationModalData = {
      title: 'Confirm Action',
      message: 'Proceed?',
    };

    it('emits true when confirmed and completes', () => {
      const results: boolean[] = [];
      let completed = false;
      const events: Array<ModalEvent<ConfirmationModalData>> = [];

      service.getModalEvents('confirmationModal').subscribe((e) => events.push(e));
      service.confirm(data).subscribe({
        next: (r) => results.push(r),
        complete: () => (completed = true),
      });

      expect(events).toEqual([{ id: 'confirmationModal', action: 'open', data }]);
      service.close('confirmationModal', true);
      expect(results).toEqual([true]);
      expect(completed).toBeTrue();
    });

    it('emits false when cancelled', () => {
      const results: boolean[] = [];
      service.confirm(data).subscribe((r) => results.push(r));

      service.close('confirmationModal', false);
      expect(results).toEqual([false]);
    });

    it('emits false when dismissed with null', () => {
      const results: boolean[] = [];
      service.confirm(data).subscribe((r) => results.push(r));

      service.close('confirmationModal', null);
      expect(results).toEqual([false]);
    });
  });

  it('delivers typed ModalRegistry payloads and results without explicit generics', () => {
    const receivedSettings: Array<FullscreenSettings | null> = [];
    service.open('fullscreenSettingsModal').subscribe((r) => receivedSettings.push(r));
    service.close('fullscreenSettingsModal', {
      delaySeconds: 15,
      entryLimit: 100,
      shuffle: true,
      repeat: false,
    });
    expect(receivedSettings).toEqual([
      { delaySeconds: 15, entryLimit: 100, shuffle: true, repeat: false },
    ]);

    const uploadEvents: Array<ModalEvent<{ files?: File[] } | undefined>> = [];
    service.getModalEvents('uploadEntryModal').subscribe((e) => uploadEvents.push(e));
    const f = new File(['x'], 'photo.png', { type: 'image/png' });
    service.open('uploadEntryModal', { files: [f] });
    expect(uploadEvents[0].data?.files).toEqual([f]);
  });

  it('H2: synchronously re-opening the same modal inside its open() result callback keeps it in openStack and emits close before the second open', () => {
    const events: ModalEvent[] = [];
    service.getModalEvents('modalA').subscribe((e) => events.push(e));

    const secondResults: Array<boolean | null> = [];
    service.open<boolean>('modalA').subscribe((result) => {
      if (result === true) {
        service.open<boolean>('modalA').subscribe((r) => secondResults.push(r));
      }
    });

    service.close('modalA', true);

    expect(service.getStackIndex('modalA')).toBeGreaterThanOrEqual(0);
    expect(events.map((e) => e.action)).toEqual(['open', 'close', 'open']);

    service.close('modalA', false);
    expect(secondResults).toEqual([false]);
    expect(service.getStackIndex('modalA')).toBe(-1);
  });

  it('H2: synchronously opening Modal B inside Modal A close subscriber emits A close before B open', () => {
    const allEvents: ModalEvent[] = [];
    service.getModalEvents('modalA').subscribe((e) => allEvents.push(e));
    service.getModalEvents('modalB').subscribe((e) => allEvents.push(e));

    service.open<boolean>('modalA').subscribe(() => {
      service.open<boolean>('modalB');
    });

    service.close('modalA', true);

    expect(allEvents.map((e) => `${e.id}:${e.action}`)).toEqual([
      'modalA:open',
      'modalA:close',
      'modalB:open',
    ]);
    expect(service.getStackIndex('modalA')).toBe(-1);
    expect(service.getStackIndex('modalB')).toBe(0);
  });

  it('assigns monotonic layer indices via getLayerIndex to avoid z-index collisions on out-of-order close', () => {
    expect(service.getLayerIndex('modalA')).toBe(-1);

    service.open<boolean>('modalA');
    service.open<boolean>('modalB');
    expect(service.getLayerIndex('modalA')).toBe(0);
    expect(service.getLayerIndex('modalB')).toBe(1);

    // Closing A while B remains open must not reuse layer 1 for the next modal C.
    service.close('modalA');
    expect(service.getLayerIndex('modalA')).toBe(-1);
    expect(service.getLayerIndex('modalB')).toBe(1);

    service.open<boolean>('modalC');
    expect(service.getLayerIndex('modalC')).toBe(2);
    expect(service.getLayerIndex('modalC')).toBeGreaterThan(service.getLayerIndex('modalB'));

    // Counter resets once all open modals are closed.
    service.close('modalB');
    service.close('modalC');
    service.open<boolean>('modalA');
    expect(service.getLayerIndex('modalA')).toBe(0);
  });
});
