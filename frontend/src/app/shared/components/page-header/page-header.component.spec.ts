import { Component } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { By } from '@angular/platform-browser';
import { provideRouter, RouterLink } from '@angular/router';
import { PageHeaderComponent } from './page-header.component';

@Component({
  template: `
    <app-page-header
      [title]="title"
      [titleTooltip]="titleTooltip"
      [backLink]="backLink"
      [backTitle]="backTitle"
      [transparent]="transparent"
    >
      <p pageHeaderSubtitle class="test-subtitle">Subtitle text</p>
      <button type="button" class="test-action">Action</button>
    </app-page-header>
  `,
  standalone: true,
  imports: [PageHeaderComponent],
})
class TestHostComponent {
  title = 'Test Page';
  titleTooltip?: string;
  backLink: string | string[] = '/dashboard';
  backTitle = 'Back to Overview';
  transparent = false;
}

describe('PageHeaderComponent', () => {
  let fixture: ComponentFixture<TestHostComponent>;
  let host: TestHostComponent;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [TestHostComponent],
      providers: [provideRouter([])],
    }).compileComponents();

    fixture = TestBed.createComponent(TestHostComponent);
    host = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('renders the title and defaults the tooltip to the title', () => {
    const heading = fixture.nativeElement.querySelector('h2.shortened-title') as HTMLElement;
    expect(heading.textContent?.trim()).toBe('Test Page');
    expect(heading.getAttribute('title')).toBe('Test Page');
  });

  it('uses titleTooltip when provided', () => {
    host.titleTooltip = 'Full Tooltip Title';
    fixture.detectChanges();

    const heading = fixture.nativeElement.querySelector('h2.shortened-title') as HTMLElement;
    expect(heading.getAttribute('title')).toBe('Full Tooltip Title');
  });

  it('binds backLink and backTitle on the back button', () => {
    const backBtnDebug = fixture.debugElement.query(By.css('a.back-btn'));
    const routerLink = backBtnDebug.injector.get(RouterLink);
    expect(routerLink.urlTree?.toString()).toBe('/dashboard');
    expect((backBtnDebug.nativeElement as HTMLElement).getAttribute('title')).toBe(
      'Back to Overview',
    );

    host.backLink = ['/dashboard/db', 'db-123'];
    host.backTitle = 'Back to Entries';
    fixture.detectChanges();

    expect(routerLink.urlTree?.toString()).toBe('/dashboard/db/db-123');
    expect((backBtnDebug.nativeElement as HTMLElement).getAttribute('title')).toBe(
      'Back to Entries',
    );
  });

  it('toggles sticky-header-transparent when transparent is true', () => {
    const header = fixture.nativeElement.querySelector('.sticky-header') as HTMLElement;
    expect(header.classList.contains('sticky-header-transparent')).toBeFalse();

    host.transparent = true;
    fixture.detectChanges();

    expect(header.classList.contains('sticky-header-transparent')).toBeTrue();
  });

  it('projects subtitle and right-side action content', () => {
    const subtitle = fixture.nativeElement.querySelector(
      '.page-header-title-wrap .test-subtitle',
    ) as HTMLElement | null;
    expect(subtitle?.textContent?.trim()).toBe('Subtitle text');

    const action = fixture.nativeElement.querySelector(
      '.sticky-header > .test-action',
    ) as HTMLElement | null;
    expect(action?.textContent?.trim()).toBe('Action');
  });
});
