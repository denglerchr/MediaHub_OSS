import { ComponentFixture, TestBed } from '@angular/core/testing';
import { FormBuilder, FormGroup, ReactiveFormsModule } from '@angular/forms';
import { ContentType } from '../../../core/models/enums';
import { DatabaseMediaConfigComponent } from './database-media-config.component';

describe('DatabaseMediaConfigComponent', () => {
  let fixture: ComponentFixture<DatabaseMediaConfigComponent>;
  let component: DatabaseMediaConfigComponent;
  let formGroup: FormGroup;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [ReactiveFormsModule, DatabaseMediaConfigComponent],
    }).compileComponents();

    const fb = TestBed.inject(FormBuilder);
    formGroup = fb.group({
      auto_conversion: [''],
      create_preview: [true],
    });

    fixture = TestBed.createComponent(DatabaseMediaConfigComponent);
    component = fixture.componentInstance;
    component.formGroup = formGroup;
  });

  it('renders image configuration with grid layout and h4 heading by default', () => {
    fixture.componentRef.setInput('contentType', ContentType.Image);
    fixture.detectChanges();

    const el: HTMLElement = fixture.nativeElement;
    const heading = el.querySelector('h4') as HTMLElement;
    expect(heading.textContent?.trim()).toBe('Image Configuration');
    expect(heading.classList.contains('sub-heading')).toBeFalse();

    const container = el.querySelector('[class*="form-grid"]') as HTMLElement;
    expect(container).not.toBeNull();

    const options = Array.from(el.querySelectorAll('#auto_conversion_image option')).map((opt) =>
      opt.getAttribute('value'),
    );
    expect(options).toEqual(['', 'image/jpeg', 'image/webp', 'image/avif']);

    const toggle = el.querySelector('#create_previews_image') as HTMLInputElement;
    expect(toggle.checked).toBeTrue();
    expect(el.querySelector('small.text-muted')?.textContent?.trim()).toBe(
      'Converts uploads to the selected format.',
    );
  });

  it('renders audio configuration with stacked layout and Settings sub-heading', () => {
    fixture.componentRef.setInput('contentType', ContentType.Audio);
    fixture.componentRef.setInput('layout', 'stacked');
    fixture.componentRef.setInput('headingStyle', 'sub-heading');
    fixture.componentRef.setInput('headingSuffix', 'Settings');
    fixture.detectChanges();

    const el: HTMLElement = fixture.nativeElement;
    const heading = el.querySelector('h4') as HTMLElement;
    expect(heading.textContent?.trim()).toBe('Audio Settings');
    expect(heading.classList.contains('sub-heading')).toBeTrue();
    expect(el.querySelector('.form-grid')).toBeNull();

    const options = Array.from(el.querySelectorAll('#auto_conversion_audio option')).map((opt) =>
      opt.getAttribute('value'),
    );
    expect(options).toEqual(['', 'audio/flac', 'audio/opus']);
  });

  it('renders video configuration with transcoding warning hint and updates form controls', () => {
    fixture.componentRef.setInput('contentType', ContentType.Video);
    fixture.detectChanges();

    const el: HTMLElement = fixture.nativeElement;
    expect(el.querySelector('h4')?.textContent?.trim()).toBe('Video Configuration');

    const select = el.querySelector('#auto_conversion_video') as HTMLSelectElement;
    const options = Array.from(select.querySelectorAll('option')).map((opt) =>
      opt.getAttribute('value'),
    );
    expect(options).toEqual(['', 'video/mp4', 'video/webm']);

    expect(el.querySelector('small.text-muted')?.textContent?.trim()).toBe(
      'Converts uploads to the selected format. Transcoding video files can take significant time.',
    );

    select.value = 'video/mp4';
    select.dispatchEvent(new Event('change'));
    const toggle = el.querySelector('#create_previews_video') as HTMLInputElement;
    toggle.click();
    fixture.detectChanges();

    expect(formGroup.get('auto_conversion')?.value).toBe('video/mp4');
    expect(formGroup.get('create_preview')?.value).toBeFalse();
  });

  it('renders generic file notice when content type does not support auto-preview', () => {
    fixture.componentRef.setInput('contentType', ContentType.File);
    fixture.componentRef.setInput('headingStyle', 'sub-heading');
    fixture.componentRef.setInput('headingSuffix', 'Settings');
    fixture.detectChanges();

    const el: HTMLElement = fixture.nativeElement;
    const heading = el.querySelector('h4') as HTMLElement;
    expect(heading.textContent?.trim()).toBe('File Settings');
    expect(heading.classList.contains('sub-heading')).toBeTrue();
    expect(el.querySelector('p.text-muted')?.textContent?.trim()).toBe(
      "No specific configuration is available for 'Generic File' databases.",
    );
    expect(el.querySelector('select')).toBeNull();
  });
});
