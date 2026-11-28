import { CommonModule } from '@angular/common';
import { Component, Input } from '@angular/core';
import { FormGroup, ReactiveFormsModule } from '@angular/forms';
import { ContentType } from '../../../core/models/enums';
import {
  AutoConversionOption,
  getAutoConversionOptions,
  getMediaConfigHint,
  supportsAutoPreview,
} from '../../../shared/utils/entry-domain';
import { ToggleSwitchComponent } from '../../../shared/components/toggle-switch/toggle-switch.component';

const CONTENT_TYPE_TITLES: Readonly<Record<string, string>> = {
  [ContentType.Image]: 'Image',
  [ContentType.Audio]: 'Audio',
  [ContentType.Video]: 'Video',
  [ContentType.File]: 'File',
};

@Component({
  selector: 'app-database-media-config',
  standalone: true,
  imports: [CommonModule, ReactiveFormsModule, ToggleSwitchComponent],
  templateUrl: './database-media-config.component.html',
  styles: [
    `
      :host {
        display: block;
      }

      .sub-heading {
        color: var(--text-muted);
        margin-top: 2rem;
        margin-bottom: 1rem;
        font-weight: 600;
      }

      h4:not(.sub-heading) {
        margin-bottom: 1rem;
        border-bottom: 1px solid var(--border-color);
        padding-bottom: 0.5rem;
      }

      .form-grid {
        display: grid;
        grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
        gap: 1.5rem;
      }
    `,
  ],
})
export class DatabaseMediaConfigComponent {
  @Input() formGroup!: FormGroup;
  @Input() contentType: ContentType | string = '';
  @Input() layout: 'grid' | 'stacked' = 'grid';
  @Input() headingStyle: 'h4' | 'sub-heading' = 'h4';
  @Input() headingSuffix: 'Configuration' | 'Settings' = 'Configuration';

  get supportsPreview(): boolean {
    return supportsAutoPreview(this.contentType);
  }

  get typeTitle(): string {
    if (
      typeof this.contentType === 'string' &&
      Object.prototype.hasOwnProperty.call(CONTENT_TYPE_TITLES, this.contentType)
    ) {
      return CONTENT_TYPE_TITLES[this.contentType];
    }
    return 'File';
  }

  get conversionOptions(): readonly AutoConversionOption[] {
    return getAutoConversionOptions(this.contentType);
  }

  get conversionHint(): string {
    return getMediaConfigHint(this.contentType);
  }

  trackByValue(_index: number, option: AutoConversionOption): string {
    return option.value;
  }
}
