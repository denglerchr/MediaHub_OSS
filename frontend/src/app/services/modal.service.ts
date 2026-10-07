// frontend/src/app/services/modal.service.ts

import { Injectable } from '@angular/core';
import { Subject, Observable } from 'rxjs';
import { filter } from 'rxjs/operators';

export interface ModalEvent {
  id: string;
  action: 'open' | 'close';
  data?: any;
}

/**
 * FE-019: a synchronous veto registered by modal content. Return `false` to block the
 * dismissal (it is consulted for every close path — in-content buttons, ESC, X and
 * overlay). The guard is expected to tell the user why the modal stays open.
 */
export type ModalCloseGuard = () => boolean;

@Injectable({
  providedIn: 'root',
})
export class ModalService {
  private modalEventSubject = new Subject<ModalEvent>();
  private resultSubjects = new Map<string, Subject<unknown>>();
  private closeGuards = new Map<string, ModalCloseGuard>();
  // N-D4: ids of the currently open modals in open order (topmost last). With
  // stacked modals (FE-019 flow) every ModalComponent listens on `document`, so
  // only the topmost one may trap Tab/Escape — otherwise they fight over focus.
  private openStack: string[] = [];

  constructor() {}

  /**
   * N-D4: true when `id` is the topmost (last-opened) currently open modal.
   */
  isTopmostOpen(id: string): boolean {
    return this.openStack.length > 0 && this.openStack[this.openStack.length - 1] === id;
  }

  /**
   * N-D4: true while at least one modal is open. The `body.modal-open` scroll lock
   * stays on until the *last* open modal closes.
   */
  hasOpenModals(): boolean {
    return this.openStack.length > 0;
  }

  /**
   * N-D4: drops `id` from the open stack without emitting events — cleanup for a
   * modal component that is destroyed while still open.
   */
  release(id: string): void {
    const idx = this.openStack.indexOf(id);
    if (idx !== -1) {
      this.openStack.splice(idx, 1);
    }
  }

  /**
   * Opens a modal and returns an Observable that emits the result on close.
   * @param id The unique ID of the modal to open.
   * @param data Optional data to pass to the modal.
   * @returns An Observable that emits the typed result on confirm and `null` on
   * cancel/dismissal. It always completes when the modal closes (or is re-opened).
   */
  open<T = boolean>(id: string, data?: any): Observable<T | null> {
    // FE-019: a re-open must not orphan the previous result stream — its consumers
    // would hang forever. Cancel and complete it before replacing it.
    const existing = this.resultSubjects.get(id);
    if (existing) {
      existing.next(null);
      existing.complete();
      this.resultSubjects.delete(id);
    }

    const subject = new Subject<T | null>();
    this.resultSubjects.set(id, subject as Subject<unknown>);
    // N-D4: track the stacking order (a re-open moves the id to the top).
    this.release(id);
    this.openStack.push(id);
    this.modalEventSubject.next({ id, action: 'open', data });
    return subject.asObservable();
  }

  /**
   * Emits a result to the result subject without closing the modal.
   * @param id The modal the result belongs to (id-anchored — FE-019).
   */
  emitResult<T>(id: string, result: T): void {
    const subject = this.resultSubjects.get(id);
    if (subject) {
      subject.next(result);
    }
  }

  /**
   * Closes the modal with the given id and emits the result.
   * @param id The modal to close (id-anchored — FE-019).
   * @param result The result to emit (typed per modal; `null` for dismissal).
   */
  close<T = boolean | null>(id: string, result: T | null = null): void {
    // FE-019: content can veto dismissal (covers ESC/X/overlay too, since all close
    // paths funnel through here).
    const guard = this.closeGuards.get(id);
    if (guard && !guard()) {
      return;
    }

    // N-D4: the stack is popped *before* the close event so the handlers can ask
    // hasOpenModals() about the post-close state (body scroll lock ownership).
    this.release(id);

    const subject = this.resultSubjects.get(id);
    if (subject) {
      subject.next(result);
      subject.complete();
      this.resultSubjects.delete(id);
    }
    // Broadcast the close event for the modal component to hide itself and for the
    // content component to tear down (media/object URLs — FE-023/FE-024).
    this.modalEventSubject.next({ id, action: 'close' });
  }

  /**
   * FE-019: lets modal content block dismissal regardless of how it was triggered.
   */
  registerCloseGuard(id: string, guard: ModalCloseGuard): void {
    this.closeGuards.set(id, guard);
  }

  unregisterCloseGuard(id: string): void {
    this.closeGuards.delete(id);
  }

  /**
   * Allows a modal component to subscribe to its specific open/close events.
   */
  getModalEvents(id: string): Observable<ModalEvent> {
    return this.modalEventSubject.asObservable().pipe(filter((event) => event.id === id));
  }
}
