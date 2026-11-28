import { ComponentFixture, TestBed } from '@angular/core/testing';
import { EMPTY } from 'rxjs';
import { EntryThumbnailComponent } from './entry-thumbnail.component';
import { Entry } from '../../../core/models/entry.model';
import { EntryStatus } from '../../../core/models/enums';
import { ImageCacheService } from '../../../core/services/image-cache.service';

describe('EntryThumbnailComponent', () => {
  let fixture: ComponentFixture<EntryThumbnailComponent>;
  let component: EntryThumbnailComponent;

  const makeEntry = (status: EntryStatus | string = EntryStatus.Ready): Entry => ({
    id: 42,
    timestamp: 1700000000,
    created_at: 1700000000,
    updated_at: 1700000000,
    status,
  });

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [EntryThumbnailComponent],
      providers: [
        {
          provide: ImageCacheService,
          useValue: {
            getBlobUrl: () => EMPTY,
            getImage: () => EMPTY,
            retain: () => {},
            release: () => {},
          },
        },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(EntryThumbnailComponent);
    component = fixture.componentInstance;
  });

  it('renders QUEUED placeholder in grid variant', () => {
    fixture.componentRef.setInput('entry', makeEntry(EntryStatus.Queued));
    fixture.componentRef.setInput('variant', 'grid');
    fixture.detectChanges();

    const host = fixture.nativeElement as HTMLElement;
    expect(host.classList.contains('variant-grid')).toBeTrue();
    const placeholder = host.querySelector('.grid-card-placeholder.status-queued');
    expect(placeholder).not.toBeNull();
    expect(placeholder?.textContent).toContain('QUEUED');
    expect(host.querySelector('img')).toBeNull();
  });

  it('renders PROCESSING placeholder with spinning icon in grid variant', () => {
    fixture.componentRef.setInput('entry', makeEntry(EntryStatus.Processing));
    fixture.componentRef.setInput('variant', 'grid');
    fixture.detectChanges();

    const host = fixture.nativeElement as HTMLElement;
    const placeholder = host.querySelector('.grid-card-placeholder.status-processing');
    expect(placeholder).not.toBeNull();
    expect(placeholder?.querySelector('svg.processing-icon')).not.toBeNull();
    expect(placeholder?.textContent).toContain('PROCESSING');
  });

  it('renders ERROR placeholder in grid variant', () => {
    fixture.componentRef.setInput('entry', makeEntry(EntryStatus.Error));
    fixture.componentRef.setInput('variant', 'grid');
    fixture.detectChanges();

    const host = fixture.nativeElement as HTMLElement;
    const placeholder = host.querySelector('.grid-card-placeholder.status-error');
    expect(placeholder).not.toBeNull();
    expect(placeholder?.textContent).toContain('ERROR');
  });

  it('renders ready image in grid variant and forwards aspectLoaded and imageError', () => {
    fixture.componentRef.setInput('entry', makeEntry(EntryStatus.Ready));
    fixture.componentRef.setInput('previewUrl', '/api/preview/42');
    fixture.componentRef.setInput('variant', 'grid');
    fixture.componentRef.setInput('failed', false);
    fixture.detectChanges();

    const host = fixture.nativeElement as HTMLElement;
    const img = host.querySelector('img.grid-card-img') as HTMLImageElement | null;
    expect(img).not.toBeNull();
    expect(img?.getAttribute('alt')).toBe('Preview for 42');

    const aspectSpy = jasmine.createSpy('aspectLoaded');
    const errorSpy = jasmine.createSpy('imageError');
    component.aspectLoaded.subscribe(aspectSpy);
    component.imageError.subscribe(errorSpy);

    component.aspectLoaded.emit(1.5);
    component.imageError.emit();
    expect(aspectSpy).toHaveBeenCalledWith(1.5);
    expect(errorSpy).toHaveBeenCalled();
  });

  it('renders NO PREVIEW fallback when failed is true in grid variant', () => {
    fixture.componentRef.setInput('entry', makeEntry(EntryStatus.Ready));
    fixture.componentRef.setInput('variant', 'grid');
    fixture.componentRef.setInput('failed', true);
    fixture.detectChanges();

    const host = fixture.nativeElement as HTMLElement;
    expect(host.querySelector('img')).toBeNull();
    const placeholder = host.querySelector('.grid-card-placeholder');
    expect(placeholder).not.toBeNull();
    expect(placeholder?.textContent).toContain('NO PREVIEW');
  });

  it('renders list variant placeholders with tooltips and emits thumbnailClick on image click', () => {
    fixture.componentRef.setInput('entry', makeEntry(EntryStatus.Queued));
    fixture.componentRef.setInput('variant', 'list');
    fixture.detectChanges();

    const host = fixture.nativeElement as HTMLElement;
    expect(host.classList.contains('variant-list')).toBeTrue();
    expect(
      host.querySelector('.list-preview-placeholder.status-queued')?.getAttribute('title'),
    ).toBe('Queued...');

    fixture.componentRef.setInput('entry', makeEntry(EntryStatus.Processing));
    fixture.detectChanges();
    expect(
      host.querySelector('.list-preview-placeholder.status-processing')?.getAttribute('title'),
    ).toBe('Processing...');

    fixture.componentRef.setInput('entry', makeEntry(EntryStatus.Error));
    fixture.detectChanges();
    expect(
      host.querySelector('.list-preview-placeholder.status-error')?.getAttribute('title'),
    ).toBe('Processing Error');

    fixture.componentRef.setInput('entry', makeEntry(EntryStatus.Ready));
    fixture.componentRef.setInput('failed', true);
    fixture.detectChanges();
    expect(host.querySelector('.list-preview-placeholder')?.getAttribute('title')).toBe(
      'No Preview Available',
    );

    fixture.componentRef.setInput('failed', false);
    fixture.componentRef.setInput('previewUrl', '/api/preview/42');
    fixture.detectChanges();

    const clickSpy = jasmine.createSpy('thumbnailClick');
    component.thumbnailClick.subscribe(clickSpy);
    const img = host.querySelector('img.list-preview-img') as HTMLImageElement;
    expect(img).not.toBeNull();
    img.click();
    expect(clickSpy).toHaveBeenCalledTimes(1);
  });
});
