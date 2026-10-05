// frontend/src/app/models/enums.ts

export enum ContentType {
  Image = 'image',
  Audio = 'audio',
  Video = 'video',
  File = 'file'
}

export enum EntryStatus {
  Processing = 'processing',
  Queued = 'queued',
  Ready = 'ready',
  Error = 'error',
  Deleted = `deleted`
}