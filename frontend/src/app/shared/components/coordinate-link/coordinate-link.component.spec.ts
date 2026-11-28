import { ComponentFixture, TestBed } from '@angular/core/testing';
import { CoordinateLinkComponent } from './coordinate-link.component';

describe('CoordinateLinkComponent', () => {
  let fixture: ComponentFixture<CoordinateLinkComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [CoordinateLinkComponent],
    }).compileComponents();

    fixture = TestBed.createComponent(CoordinateLinkComponent);
  });

  it('renders an OpenStreetMap link with formatted coordinates in detail variant', () => {
    fixture.componentRef.setInput('value', { latitude: 49.611621, longitude: 6.131935 });
    fixture.componentRef.setInput('variant', 'detail');
    fixture.detectChanges();

    const anchor = fixture.nativeElement.querySelector('a') as HTMLAnchorElement | null;
    expect(anchor).not.toBeNull();
    expect(anchor?.getAttribute('href')).toBe(
      'https://www.openstreetmap.org/?mlat=49.611621&mlon=6.131935#map=16/49.611621/6.131935',
    );
    expect(anchor?.getAttribute('target')).toBe('_blank');
    expect(anchor?.getAttribute('rel')).toBe('noopener noreferrer');
    expect(anchor?.getAttribute('title')).toBe('Open location in OpenStreetMap');
    expect(anchor?.classList.contains('coordinate-detail-link')).toBeTrue();
    expect(anchor?.textContent).toContain('49.611621, 6.131935');
    expect(anchor?.querySelector('.external-icon')?.textContent).toContain('↗');
  });

  it('renders table variant without external icon and stops propagation when configured', () => {
    fixture.componentRef.setInput('value', { latitude: 10.5, longitude: -20.25 });
    fixture.componentRef.setInput('variant', 'table');
    fixture.componentRef.setInput('stopPropagation', true);
    fixture.detectChanges();

    const anchor = fixture.nativeElement.querySelector('a') as HTMLAnchorElement;
    expect(anchor.classList.contains('coordinate-table-link')).toBeTrue();
    expect(anchor.getAttribute('title')).toBe('View in OpenStreetMap');
    expect(anchor.querySelector('.external-icon')).toBeNull();
    expect(anchor.textContent).toContain('10.500000, -20.250000');

    const event = new MouseEvent('click', { bubbles: true, cancelable: true });
    spyOn(event, 'stopPropagation');
    anchor.dispatchEvent(event);
    expect(event.stopPropagation).toHaveBeenCalled();
  });

  it('does not stop propagation when stopPropagation is false', () => {
    fixture.componentRef.setInput('value', { latitude: 1, longitude: 2 });
    fixture.componentRef.setInput('stopPropagation', false);
    fixture.detectChanges();

    const anchor = fixture.nativeElement.querySelector('a') as HTMLAnchorElement;
    const event = new MouseEvent('click', { bubbles: true, cancelable: true });
    spyOn(event, 'stopPropagation');
    anchor.dispatchEvent(event);
    expect(event.stopPropagation).not.toHaveBeenCalled();
  });

  it('renders fallback text when value is invalid and omits text when fallback is empty', () => {
    fixture.componentRef.setInput('value', null);
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('a')).toBeNull();
    expect(fixture.nativeElement.textContent.trim()).toBe('N/A');

    fixture.componentRef.setInput('value', { latitude: 'bad', longitude: 0 });
    fixture.componentRef.setInput('fallback', 'Unknown');
    fixture.detectChanges();
    expect(fixture.nativeElement.textContent.trim()).toBe('Unknown');

    fixture.componentRef.setInput('fallback', '');
    fixture.detectChanges();
    expect(fixture.nativeElement.textContent.trim()).toBe('');
  });

  it('enforces WGS84 latitude/longitude bounds and updates cached state on input changes', () => {
    fixture.componentRef.setInput('value', { latitude: 91, longitude: 0 });
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('a')).toBeNull();
    expect(fixture.nativeElement.textContent.trim()).toBe('N/A');

    fixture.componentRef.setInput('value', { latitude: 0, longitude: -181 });
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('a')).toBeNull();

    fixture.componentRef.setInput('value', { latitude: -90, longitude: 180 });
    fixture.detectChanges();
    const anchor = fixture.nativeElement.querySelector('a') as HTMLAnchorElement | null;
    expect(anchor).not.toBeNull();
    expect(anchor?.textContent).toContain('-90.000000, 180.000000');
  });
});
