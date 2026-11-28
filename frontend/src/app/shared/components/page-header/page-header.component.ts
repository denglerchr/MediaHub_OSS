import { CommonModule } from '@angular/common';
import { ChangeDetectionStrategy, Component, Input } from '@angular/core';
import { RouterLink } from '@angular/router';

@Component({
  selector: 'app-page-header',
  templateUrl: './page-header.component.html',
  styleUrls: ['./page-header.component.css'],
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule, RouterLink],
})
export class PageHeaderComponent {
  @Input() title = '';
  @Input() titleTooltip?: string;
  @Input() backLink: string | string[] = '/dashboard';
  @Input() backTitle = 'Back to Overview';
  @Input() transparent = false;
}
