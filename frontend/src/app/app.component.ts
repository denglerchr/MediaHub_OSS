import { Component } from '@angular/core';
import { RouterOutlet } from '@angular/router';
import { ThemeService } from './core/services/theme.service';
import { NotificationHostComponent } from './shared/components/notification-host/notification-host.component';
import { ConfirmationModalComponent } from './shared/components/confirmation-modal/confirmation-modal.component';

@Component({
  selector: 'app-root',
  templateUrl: './app.component.html',
  styleUrls: ['./app.component.css'],
  standalone: true,
  imports: [RouterOutlet, NotificationHostComponent, ConfirmationModalComponent],
})
export class AppComponent {
  title = 'MediaHub';

  constructor(private themeService: ThemeService) {}
}
