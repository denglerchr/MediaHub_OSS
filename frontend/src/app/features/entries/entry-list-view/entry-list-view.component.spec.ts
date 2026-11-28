import { Component } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { By } from '@angular/platform-browser';
import { EMPTY } from 'rxjs';
import { EntryListViewComponent } from './entry-list-view.component';
import { EntryService } from '../../../core/services/entry.service';
import { ImageCacheService } from '../../../core/services/image-cache.service';
import { Entry, User } from '../../../core/models';

@Component({
  template: `
    <app-entry-list-view
      [entries]="entries"
      [tableColumns]="tableColumns"
      [user]="user"
      [dbId]="dbId"
    ></app-entry-list-view>
  `,
  imports: [EntryListViewComponent],
})
class TestHostComponent {
  entries: Entry[] = [];
  tableColumns: string[] = ['Preview', 'id', 'status', 'actions'];
  user: User | null = null;
  dbId: string | null = 'db1';
}

describe('EntryListViewComponent', () => {
  let fixture: ComponentFixture<TestHostComponent>;
  let host: TestHostComponent;

  const makeEntry = (id: number, status = 'ready'): Entry =>
    ({
      id,
      timestamp: 1000 * id,
      created_at: 1000 * id,
      updated_at: 1000 * id,
      status,
      mime_type: 'image/jpeg',
      filename: `entry-${id}.jpg`,
    }) as Entry;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [TestHostComponent],
      providers: [
        {
          provide: EntryService,
          useValue: {
            getEntryPreviewUrl: (dbId: string, id: number) => `/preview/${dbId}/${id}`,
          },
        },
        {
          provide: ImageCacheService,
          useValue: {
            getImage: () => EMPTY,
          },
        },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(TestHostComponent);
    host = fixture.componentInstance;
  });

  it('retains failed preview IDs across entries refreshes for the same dbId and prunes missing IDs', () => {
    host.entries = [makeEntry(1), makeEntry(2)];
    fixture.detectChanges();

    const listView = fixture.debugElement.query(By.css('app-entry-list-view'))
      .componentInstance as EntryListViewComponent;

    listView.onImageError(1);
    listView.onImageError(2);
    expect(listView.failedImageIds.has(1)).toBeTrue();
    expect(listView.failedImageIds.has(2)).toBeTrue();

    // Refresh entries on same dbId: entry 1 remains, entry 2 is removed, entry 3 is added
    host.entries = [makeEntry(1), makeEntry(3)];
    fixture.detectChanges();

    expect(listView.failedImageIds.has(1)).toBeTrue();
    expect(listView.failedImageIds.has(2)).toBeFalse();
    expect(listView.failedImageIds.size).toBe(1);
  });

  it('clears failed preview IDs when dbId changes', () => {
    host.entries = [makeEntry(1), makeEntry(2)];
    fixture.detectChanges();

    const listView = fixture.debugElement.query(By.css('app-entry-list-view'))
      .componentInstance as EntryListViewComponent;

    listView.onImageError(1);
    expect(listView.failedImageIds.has(1)).toBeTrue();

    host.dbId = 'db2';
    fixture.detectChanges();

    expect(listView.failedImageIds.size).toBe(0);
  });

  it('resolves scoped edit and delete permissions for user and dbId', () => {
    host.user = {
      id: 'u1',
      username: 'scoped',
      is_admin: false,
      permissions: [
        {
          database_id: 'db1',
          can_view: true,
          can_create: false,
          can_edit: true,
          can_delete: false,
          can_admin: false,
        },
      ],
    } as unknown as User;
    fixture.detectChanges();

    const listView = fixture.debugElement.query(By.css('app-entry-list-view'))
      .componentInstance as EntryListViewComponent;

    expect(listView.canEdit).toBeTrue();
    expect(listView.canDelete).toBeFalse();

    host.dbId = 'db2';
    fixture.detectChanges();

    expect(listView.canEdit).toBeFalse();
    expect(listView.canDelete).toBeFalse();
  });
});
