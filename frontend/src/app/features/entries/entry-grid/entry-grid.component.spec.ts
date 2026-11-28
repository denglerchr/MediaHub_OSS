// frontend/src/app/components/entry-grid/entry-grid.component.spec.ts
// Regression spec for FE-027 (Phase H correction): the group layout metrics must
// be computed against the *real* wrap threshold on first paint. recalculate()
// used to run only from ngOnChanges — before ngOnInit/ngAfterViewInit measured
// the container — so `isLarge` was decided with the default threshold 8 until the
// first resize or image-load.
import { Component } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { By } from '@angular/platform-browser';
import { EntryGridComponent } from './entry-grid.component';
import { EntryService } from '../../../core/services/entry.service';
import { Entry } from '../../../core/models';

@Component({
  template: `<app-entry-grid [entries]="entries" [dbId]="dbId"></app-entry-grid>`,
  imports: [EntryGridComponent],
})
class TestHostComponent {
  entries: Entry[] = [];
  dbId: string | null = 'db1';
}

describe('EntryGridComponent — FE-027 group layout metrics at init', () => {
  let fixture: ComponentFixture<TestHostComponent>;
  let host: TestHostComponent;

  const makeEntry = (id: number): Entry =>
    ({
      id,
      timestamp: 1000,
      status: 'queued',
      mime_type: 'image/jpeg',
      filename: `img-${id}.jpg`,
    }) as Entry;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [TestHostComponent],
      providers: [
        {
          provide: EntryService,
          useValue: { getEntryPreviewUrl: (dbId: string, id: number) => `/preview/${dbId}/${id}` },
        },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(TestHostComponent);
    host = fixture.componentInstance;
    // A narrow container: the wrap threshold is width / tileHeight (200 / 150 or
    // 200 / 100) — far below the pre-fix default of 8 that ngOnChanges used.
    // The grid element exists right after createComponent (Ivy instantiates the
    // template eagerly), i.e. before the first change detection runs any hook.
    const gridEl = fixture.debugElement.query(By.css('app-entry-grid'))
      .nativeElement as HTMLElement;
    spyOn(gridEl, 'getBoundingClientRect').and.returnValue({ width: 200 } as DOMRect);
  });

  it('FE-027: a group above the real wrap threshold renders large-group on first paint', () => {
    // Three square entries → group aspect ratio 3.0 > threshold (~1.3–2).
    host.entries = [makeEntry(1), makeEntry(2), makeEntry(3)];

    fixture.detectChanges(); // first paint: ngOnChanges → ngOnInit → ngAfterViewInit

    const group: HTMLElement | null = fixture.nativeElement.querySelector('.date-group');
    expect(group).not.toBeNull();
    expect(group!.classList.contains('large-group')).toBeTrue();
  });

  it('FE-027: a group below the real wrap threshold does not render large-group', () => {
    // A single square entry → aspect ratio 1.0 < threshold on a narrow container.
    host.entries = [makeEntry(1)];

    fixture.detectChanges();

    const group: HTMLElement | null = fixture.nativeElement.querySelector('.date-group');
    expect(group).not.toBeNull();
    expect(group!.classList.contains('large-group')).toBeFalse();
  });

  it('clears aspectRatios and failedImageIds when dbId changes', () => {
    host.entries = [makeEntry(1)];
    fixture.detectChanges();

    const gridComp = fixture.debugElement.query(By.css('app-entry-grid'))
      .componentInstance as EntryGridComponent;
    gridComp.onAspectLoaded(1, 2.0);
    gridComp.onImageError(1);
    expect(gridComp.aspectRatios.get(1)).toBe(2.0);
    expect(gridComp.failedImageIds.has(1)).toBeTrue();

    host.dbId = 'db2';
    fixture.detectChanges();

    expect(gridComp.aspectRatios.size).toBe(0);
    expect(gridComp.failedImageIds.size).toBe(0);
  });

  it('M3: onAspectLoaded skips recalculation when loaded ratio matches metadata dimensions', () => {
    const entryWithDimensions = {
      ...makeEntry(10),
      media_fields: { width: 800, height: 400 },
    } as Entry;
    host.entries = [entryWithDimensions];
    fixture.detectChanges();

    const gridComp = fixture.debugElement.query(By.css('app-entry-grid'))
      .componentInstance as EntryGridComponent;
    const recalcSpy = spyOn(
      gridComp as unknown as { recalculateGroupAspectRatio: (g: unknown) => void },
      'recalculateGroupAspectRatio',
    ).and.callThrough();

    // Loaded ratio matches 800 / 400 = 2.0 from metadata
    gridComp.onAspectLoaded(10, 2.0);
    expect(recalcSpy).not.toHaveBeenCalled();
    expect(gridComp.aspectRatios.has(10)).toBeFalse();

    // Different ratio triggers recalculation for the owning group only
    gridComp.onAspectLoaded(10, 1.5);
    expect(recalcSpy).toHaveBeenCalledTimes(1);
    expect(gridComp.aspectRatios.get(10)).toBe(1.5);
  });
});
