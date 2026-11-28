// frontend/src/app/components/fullscreen-settings-modal/fullscreen-settings-modal.component.spec.ts
// Regression spec for N7 (Phase D): the cancel path must close with `null`, keeping
// the modal service contract coherent (`open<FullscreenSettings>` → `FullscreenSettings | null`).
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { NO_ERRORS_SCHEMA } from '@angular/core';
import { FullscreenSettingsModalComponent } from './fullscreen-settings-modal.component';
import { ModalComponent } from '../../../shared/components/modal/modal.component';
import { ModalService } from '../../../core/services/modal.service';

describe('FullscreenSettingsModalComponent — N7 T | null result contract', () => {
  let fixture: ComponentFixture<FullscreenSettingsModalComponent>;
  let component: FullscreenSettingsModalComponent;
  let modalService: ModalService;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [FullscreenSettingsModalComponent],
      providers: [ModalService],
    })
      .overrideComponent(FullscreenSettingsModalComponent, {
        remove: { imports: [ModalComponent] },
        add: { schemas: [NO_ERRORS_SCHEMA] },
      })
      .compileComponents();

    fixture = TestBed.createComponent(FullscreenSettingsModalComponent);
    component = fixture.componentInstance;
    modalService = TestBed.inject(ModalService);
    fixture.detectChanges();
  });

  it('N7: cancelling closes the modal with `null`, not `false`', () => {
    const closeSpy = spyOn(modalService, 'close').and.callThrough();

    component.closeModal();

    expect(closeSpy).toHaveBeenCalledWith(FullscreenSettingsModalComponent.MODAL_ID, null);
  });

  it('N7 guard: confirming still closes with the configured settings', () => {
    const closeSpy = spyOn(modalService, 'close').and.callThrough();

    component.settingsForm.setValue({
      delaySeconds: 15,
      entryLimit: 25,
      shuffle: true,
      repeat: false,
    });
    component.onSubmit();

    expect(closeSpy).toHaveBeenCalledWith(FullscreenSettingsModalComponent.MODAL_ID, {
      delaySeconds: 15,
      entryLimit: 25,
      shuffle: true,
      repeat: false,
    });
  });

  it('validates entryLimit as a whole integer in [1, 1000]', () => {
    const entryLimitCtrl = component.settingsForm.get('entryLimit')!;

    for (const invalid of [0, -5, 1001, 12.5]) {
      entryLimitCtrl.setValue(invalid);
      expect(entryLimitCtrl.invalid).withContext(`expected ${invalid} to be invalid`).toBeTrue();
    }

    for (const valid of [1, 50, 1000]) {
      entryLimitCtrl.setValue(valid);
      expect(entryLimitCtrl.valid).withContext(`expected ${valid} to be valid`).toBeTrue();
    }
  });
});
