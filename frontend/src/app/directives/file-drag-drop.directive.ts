// frontend/src/app/directives/file-drag-drop.directive.ts
import { Directive, HostBinding, HostListener, Output, EventEmitter } from '@angular/core';

@Directive({
  selector: '[appFileDragDrop]',
  standalone: true,
})
export class FileDragDropDirective {
  @Output() fileDropped = new EventEmitter<File>();
  @Output() filesDropped = new EventEmitter<File[]>();

  // Binds to the class 'file-drag-over' on the host element.
  // This allows CSS to style the element when a file is hovering.
  @HostBinding('class.file-drag-over') fileOver: boolean = false;

  constructor() {}

  /**
   * Handle the dragover event.
   * We must prevent default to allow dropping.
   */
  @HostListener('dragover', ['$event'])
  onDragOver(evt: DragEvent): void {
    evt.preventDefault();
    evt.stopPropagation();
    this.fileOver = true;
  }

  /**
   * Handle dragleave to remove the visual cue.
   */
  @HostListener('dragleave', ['$event'])
  onDragLeave(evt: DragEvent): void {
    evt.preventDefault();
    evt.stopPropagation();
    this.fileOver = false;
  }

  /**
   * Handle the drop event.
   * prevents browser opening the file, resets visual cue, and emits the file.
   */
  @HostListener('drop', ['$event'])
  onDrop(evt: DragEvent): void {
    evt.preventDefault();
    evt.stopPropagation();
    this.fileOver = false;

    const files = evt.dataTransfer?.files;
    if (files && files.length > 0) {
      const fileList: File[] = [];
      for (let i = 0; i < files.length; i++) {
        fileList.push(files[i]);
      }
      this.filesDropped.emit(fileList);
      this.fileDropped.emit(files[0]);
    }
  }
}
