import { Component, DestroyRef, OnInit, inject } from '@angular/core';
import { CommonModule } from '@angular/common';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { ActivatedRoute, Router } from '@angular/router';
import { take } from 'rxjs/operators';
import { AuthService } from '../../../core/services/auth.service';
import { AuthCardComponent } from '../../../shared/components/auth-card/auth-card.component';

@Component({
  selector: 'app-auth-callback-page',
  templateUrl: './auth-callback-page.component.html',
  styleUrls: ['./auth-callback-page.component.css'],
  standalone: true,
  imports: [CommonModule, AuthCardComponent],
})
export class AuthCallbackPageComponent implements OnInit {
  private destroyRef = inject(DestroyRef);

  constructor(
    private route: ActivatedRoute,
    private router: Router,
    private authService: AuthService,
  ) {}

  ngOnInit(): void {
    this.authService
      .completeOidcCallback(this.route.snapshot.queryParamMap)
      .pipe(take(1), takeUntilDestroyed(this.destroyRef))
      .subscribe((result) => {
        if (result.status === 'success') {
          this.router.navigate(['/dashboard'], { replaceUrl: true });
          return;
        }
        this.router.navigate(['/login'], {
          ...(result.errorParam ? { queryParams: { error: result.errorParam } } : {}),
          replaceUrl: true,
        });
      });
  }
}
