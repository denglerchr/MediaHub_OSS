// frontend/src/app/directives/file-drag-drop.directive.ts
import {
  Directive,
  ElementRef,
  EventEmitter,
  HostBinding,
  HostListener,
  NgZone,
  OnDestroy,
  OnInit,
  Output,
  Renderer2,
} from '@angular/core';

@Directive({
  selector: '[appFileDragDrop]',
  standalone: true,
})
export class FileDragDropDirective implements OnInit, OnDestroy {
  @Output() fileDropped = new EventEmitter<File>();
  @Output() filesDropped = new EventEmitter<File[]>();

  // Binds to the class 'file-drag-over' on the host element.
  // This allows CSS to style the element when a file is hovering.
  @HostBinding('class.file-drag-over') fileOver: boolean = false;

  private unlistenDragOver?: () => void;

  constructor(
    private el: ElementRef<HTMLElement>,
    private ngZone: NgZone,
    private renderer: Renderer2,
  ) {}

  ngOnInit(): void {
    this.unlistenDragOver = this.ngZone.runOutsideAngular(() =>
      this.renderer.listen(this.el.nativeElement, 'dragover', (evt: DragEvent) => {
        evt.preventDefault();
        evt.stopPropagation();
        if (!this.fileOver) {
          this.ngZone.run(() => {
            this.fileOver = true;
          });
        }
      }),
    );
  }

  ngOnDestroy(): void {
    this.unlistenDragOver?.();
    this.unlistenDragOver = undefined;
  }

  /**
   * Handle dragleave to remove the visual cue.
   */
  @HostListener('dragleave', ['$event'])
  onDragLeave(evt: DragEvent): void {
    evt.preventDefault();
    evt.stopPropagation();
    const related = evt.relatedTarget as Node | null;
    if (related && this.el.nativeElement.contains(related)) {
      return;
    }
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
