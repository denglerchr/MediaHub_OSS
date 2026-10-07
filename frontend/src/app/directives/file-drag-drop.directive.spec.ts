// frontend/src/app/directives/file-drag-drop.directive.spec.ts
import { Component, DebugElement } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { By } from '@angular/platform-browser';
import { FileDragDropDirective } from './file-drag-drop.directive';

// Dummy component to host the directive for testing
@Component({
  template: `<div appFileDragDrop (fileDropped)="onFileDropped($event)">Drop Zone</div>`,
  imports: [FileDragDropDirective],
  standalone: true,
})
class TestComponent {
  lastFile: File | null = null;
  onFileDropped(file: File) {
    this.lastFile = file;
  }
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
    divEl.triggerEventHandler('dragover', {
      preventDefault: () => {},
      stopPropagation: () => {},
    });
    fixture.detectChanges();
    expect(divEl.classes['file-drag-over']).toBeTrue();
  });

  it('should remove "file-drag-over" class on dragleave', () => {
    // First trigger dragover
    divEl.triggerEventHandler('dragover', {
      preventDefault: () => {},
      stopPropagation: () => {},
    });
    fixture.detectChanges();

    // Then trigger dragleave
    divEl.triggerEventHandler('dragleave', {
      preventDefault: () => {},
      stopPropagation: () => {},
    });
    fixture.detectChanges();
    expect(divEl.classes['file-drag-over']).toBeFalsy();
  });

  it('should emit fileDropped event on drop', () => {
    const mockFile = new File([''], 'test.jpg', { type: 'image/jpeg' });
    const mockEvt = {
      preventDefault: () => {},
      stopPropagation: () => {},
      dataTransfer: {
        files: [mockFile],
      },
    };

    // Trigger drop
    divEl.triggerEventHandler('drop', mockEvt);
    fixture.detectChanges();

    expect(divEl.classes['file-drag-over']).toBeFalsy(); // Should reset class
    expect(component.lastFile).toBe(mockFile); // Should emit file
  });
});
