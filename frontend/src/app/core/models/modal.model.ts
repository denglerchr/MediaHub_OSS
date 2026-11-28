// frontend/src/app/models/modal.model.ts

import { ApiKey } from './api-key.model';

export interface ConfirmationModalData {
  title?: string;
  message: string;
  confirmText?: string;
  cancelText?: string;
  confirmButtonClass?: string;
}

export interface ApiKeyModalData {
  userId: string;
  apiKey?: ApiKey;
}

export interface UploadEntryModalData {
  files?: File[];
}

export interface FullscreenSettings {
  delaySeconds: number;
  entryLimit: number;
  shuffle: boolean;
  repeat: boolean;
}

export interface ModalRegistry {
  confirmationModal: { data: ConfirmationModalData; result: boolean };
  apiKeyModal: { data: ApiKeyModalData; result: boolean };
  uploadEntryModal: { data: UploadEntryModalData | undefined; result: boolean };
  createDatabaseModal: { data: undefined; result: boolean };
  editEntryModal: { data: undefined; result: boolean };
  entryDetailModal: { data: undefined; result: void };
  fullscreenSettingsModal: { data: undefined; result: FullscreenSettings };
}

export type ModalId = keyof ModalRegistry;
