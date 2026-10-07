// frontend/src/app/components/fullscreen-settings-modal/fullscreen-settings-modal.component.spec.ts
// Regression spec for N7 (Phase D): the cancel path must close with `null`, keeping
// the modal service contract coherent (`open<FullscreenSettings>` → `FullscreenSettings | null`).
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { NO_ERRORS_SCHEMA } from '@angular/core';
import { CommonModule } from '@angular/common';
import { ReactiveFormsModule } from '@angular/forms';
import { FullscreenSettingsModalComponent } from './fullscreen-settings-modal.component';
import { ModalService } from '../../services/modal.service';

describe('FullscreenSettingsModalComponent — N7 T | null result contract', () => {
  let fixture: ComponentFixture<FullscreenSettingsModalComponent>;
  let component: FullscreenSettingsModalComponent;
  let modalService: ModalService;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      declarations: [FullscreenSettingsModalComponent],
      imports: [CommonModule, ReactiveFormsModule],
      schemas: [NO_ERRORS_SCHEMA], // <app-modal> wrapper is not under test
      providers: [ModalService],
    }).compileComponents();

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
});
