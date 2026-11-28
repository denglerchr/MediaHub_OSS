// frontend/src/app/directives/file-drag-drop.directive.spec.ts
import { Component, DebugElement, NgZone } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { By } from '@angular/platform-browser';
import { FileDragDropDirective } from './file-drag-drop.directive';

// Dummy component to host the directive for testing
@Component({
  template: `
    <div appFileDragDrop (fileDropped)="onFileDropped($event)">
      <span class="child">Drop Zone</span>
    </div>
    <div class="outside">Outside</div>
  `,
  imports: [FileDragDropDirective],
  standalone: true,
})
class TestComponent {
  lastFile: File | null = null;
  onFileDropped(file: File) {
    this.lastFile = file;
  }
}

function createDragEvent(
  type: string,
  options: { relatedTarget?: EventTarget | null; dataTransfer?: unknown } = {},
): Event {
  const evt =
    typeof DragEvent !== 'undefined'
      ? new DragEvent(type, {
          bubbles: true,
          cancelable: true,
          relatedTarget: options.relatedTarget ?? null,
        })
      : new Event(type, { bubbles: true, cancelable: true });

  if (
    options.relatedTarget !== undefined &&
    (evt as DragEvent).relatedTarget !== options.relatedTarget
  ) {
    Object.defineProperty(evt, 'relatedTarget', { value: options.relatedTarget });
  }
  if (options.dataTransfer !== undefined) {
    Object.defineProperty(evt, 'dataTransfer', { value: options.dataTransfer });
  }
  return evt;
}

describe('FileDragDropDirective', () => {
  let fixture: ComponentFixture<TestComponent>;
  let divEl: DebugElement;
  let component: TestComponent;

  beforeEach(() => {
    TestBed.configureTestingModule({
      imports: [FileDragDropDirective, TestComponent],
    });
    fixture = TestBed.createComponent(TestComponent);
    component = fixture.componentInstance;
    divEl = fixture.debugElement.query(By.directive(FileDragDropDirective));
    fixture.detectChanges();
  });

  it('should apply "file-drag-over" class on dragover', () => {
    divEl.nativeElement.dispatchEvent(createDragEvent('dragover'));
    fixture.detectChanges();
    expect(divEl.classes['file-drag-over']).toBeTrue();
  });

  it('should keep "file-drag-over" when dragleave moves to a child element inside host', () => {
    divEl.nativeElement.dispatchEvent(createDragEvent('dragover'));
    fixture.detectChanges();
    expect(divEl.classes['file-drag-over']).toBeTrue();

    const childEl = fixture.nativeElement.querySelector('.child') as HTMLElement;
    divEl.nativeElement.dispatchEvent(createDragEvent('dragleave', { relatedTarget: childEl }));
    fixture.detectChanges();
    expect(divEl.classes['file-drag-over']).toBeTrue();
  });

  it('should remove "file-drag-over" class on external dragleave', () => {
    divEl.nativeElement.dispatchEvent(createDragEvent('dragover'));
    fixture.detectChanges();

    const outsideEl = fixture.nativeElement.querySelector('.outside') as HTMLElement;
    divEl.nativeElement.dispatchEvent(createDragEvent('dragleave', { relatedTarget: outsideEl }));
    fixture.detectChanges();
    expect(divEl.classes['file-drag-over']).toBeFalsy();
  });

  it('should keep repeated dragover events outside NgZone once fileOver is true', () => {
    const ngZone = TestBed.inject(NgZone);
    const runSpy = spyOn(ngZone, 'run').and.callThrough();

    divEl.nativeElement.dispatchEvent(createDragEvent('dragover'));
    expect(runSpy).toHaveBeenCalled();

    runSpy.calls.reset();
    divEl.nativeElement.dispatchEvent(createDragEvent('dragover'));
    divEl.nativeElement.dispatchEvent(createDragEvent('dragover'));
    expect(runSpy).not.toHaveBeenCalled();
  });

  it('should emit fileDropped event on drop', () => {
    const mockFile = new File([''], 'test.jpg', { type: 'image/jpeg' });
    const dropEvt = createDragEvent('drop', {
      dataTransfer: {
        files: [mockFile],
      },
    });

    divEl.nativeElement.dispatchEvent(dropEvt);
    fixture.detectChanges();

    expect(divEl.classes['file-drag-over']).toBeFalsy(); // Should reset class
    expect(component.lastFile).toBe(mockFile); // Should emit file
  });

  it('should unlisten dragover cleanly on fixture.destroy()', () => {
    const directive = divEl.injector.get(FileDragDropDirective);
    expect(() => fixture.destroy()).not.toThrow();

    const evt = createDragEvent('dragover');
    divEl.nativeElement.dispatchEvent(evt);
    expect(evt.defaultPrevented).toBeFalse();
    expect(directive.fileOver).toBeFalse();
  });
});
