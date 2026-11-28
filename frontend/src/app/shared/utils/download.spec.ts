import { DOWNLOAD_REVOKE_DELAY_MS, downloadBlob } from './download';

describe('downloadBlob', () => {
  it('creates an object URL, clicks a temporary anchor, removes it, and revokes URL after DOWNLOAD_REVOKE_DELAY_MS', () => {
    jasmine.clock().install();
    try {
      const blob = new Blob(['hello world'], { type: 'text/plain' });
      const createSpy = spyOn(URL, 'createObjectURL').and.returnValue('blob:mock-url');
      const revokeSpy = spyOn(URL, 'revokeObjectURL');

      const fakeAnchor = document.createElement('a');
      const clickSpy = spyOn(fakeAnchor, 'click');
      spyOn(document, 'createElement').and.returnValue(fakeAnchor);
      const appendSpy = spyOn(document.body, 'appendChild').and.callThrough();
      const removeSpy = spyOn(document.body, 'removeChild').and.callThrough();

      downloadBlob(blob, 'export.zip');

      expect(DOWNLOAD_REVOKE_DELAY_MS).toBe(30_000);
      expect(createSpy).toHaveBeenCalledWith(blob);
      expect(fakeAnchor.href).toContain('blob:mock-url');
      expect(fakeAnchor.download).toBe('export.zip');
      expect(appendSpy).toHaveBeenCalledWith(fakeAnchor);
      expect(clickSpy).toHaveBeenCalledTimes(1);
      expect(removeSpy).toHaveBeenCalledWith(fakeAnchor);
      expect(revokeSpy).not.toHaveBeenCalled();

      jasmine.clock().tick(DOWNLOAD_REVOKE_DELAY_MS - 1);
      expect(revokeSpy).not.toHaveBeenCalled();

      jasmine.clock().tick(1);
      expect(revokeSpy).toHaveBeenCalledWith('blob:mock-url');
    } finally {
      jasmine.clock().uninstall();
    }
  });
});
