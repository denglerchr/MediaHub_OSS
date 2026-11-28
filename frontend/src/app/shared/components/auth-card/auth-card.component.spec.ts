import { Component } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { AuthCardComponent } from './auth-card.component';

@Component({
  standalone: true,
  imports: [AuthCardComponent],
  template: `
    <app-auth-card [subtitle]="subtitle" [centered]="centered">
      <div class="projected-body">Projected Content</div>
    </app-auth-card>
  `,
})
class TestHostComponent {
  subtitle = '';
  centered = false;
}

describe('AuthCardComponent', () => {
  let fixture: ComponentFixture<TestHostComponent>;
  let hostComponent: TestHostComponent;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [TestHostComponent],
    }).compileComponents();

    fixture = TestBed.createComponent(TestHostComponent);
    hostComponent = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('renders MediaHub title, logo, and SWCD link', () => {
    const el = fixture.nativeElement as HTMLElement;
    const logo = el.querySelector('.auth-header img.logo-icon') as HTMLImageElement | null;
    expect(logo).not.toBeNull();
    expect(logo?.getAttribute('src')).toBe('assets/logo.svg');
    expect(el.querySelector('.auth-header h1')?.textContent?.trim()).toBe('MediaHub');

    const creditLink = el.querySelector('.author-credits a') as HTMLAnchorElement | null;
    expect(creditLink?.getAttribute('href')).toBe('https://www.swcd.lu');
    expect(creditLink?.textContent?.trim()).toBe('by SWCD');
  });

  it('renders optional subtitle only when provided', () => {
    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelector('.auth-subtitle')).toBeNull();

    hostComponent.subtitle = 'Welcome back! Please log in to continue.';
    fixture.detectChanges();

    const subtitleEl = el.querySelector('.auth-subtitle');
    expect(subtitleEl).not.toBeNull();
    expect(subtitleEl?.textContent?.trim()).toBe('Welcome back! Please log in to continue.');
  });

  it('applies centered class when centered is true and projects content', () => {
    const el = fixture.nativeElement as HTMLElement;
    const box = el.querySelector('.auth-box') as HTMLElement;
    expect(box.classList.contains('centered')).toBeFalse();
    expect(el.querySelector('.projected-body')?.textContent?.trim()).toBe('Projected Content');

    hostComponent.centered = true;
    fixture.detectChanges();
    expect(box.classList.contains('centered')).toBeTrue();
  });
});
