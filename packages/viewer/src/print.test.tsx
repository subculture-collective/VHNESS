import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { PrintReader, initialPrintPage, printPageScale, type PrintBackend } from './print.js';

describe('shared print reader', () => {
  it('keeps invalid and restored page positions within the document', () => {
    for (const value of [undefined, -1, 0, NaN, Infinity, 1.5]) expect(initialPrintPage(value)).toBe(1);
    expect(initialPrintPage(99, 4)).toBe(4);
    expect(initialPrintPage(3, 4)).toBe(3);
  });
  it('exposes semantic reader controls without requiring a PDF runtime on import', () => {
    const backend: PrintBackend = { open: () => { throw new Error('Server rendering must not fetch a PDF'); } };
    const markup = renderToStaticMarkup(<PrintReader url="/api/approved.pdf" backend={backend} title="Fixture edition" />);
    expect(markup).toContain('aria-label="Document reader"');
    expect(markup).toContain('aria-label="Fixture edition, PDF page 1"');
    expect(markup).toContain('Previous page');
    expect(markup).toContain('Readable text for this page');
    expect(markup).not.toContain('Search this document');
    expect(markup).not.toContain('Download');
    expect(markup).not.toContain('Whole page');
    expect(markup).not.toContain('Full screen');
    expect(markup).not.toContain('page-arrow');
  });
  it('offers page arrows, a whole-page fit and full screen when asked', () => {
    const backend: PrintBackend = { open: () => { throw new Error('Server rendering must not fetch a PDF'); } };
    const markup = renderToStaticMarkup(<PrintReader url="/api/approved.pdf" backend={backend} title="Fixture edition" pageArrows fitPage fullscreen />);
    expect(markup).toContain('class="page-arrow page-arrow-previous" aria-label="Previous page"');
    expect(markup).toContain('class="page-arrow page-arrow-next" aria-label="Next page"');
    expect(markup).not.toContain('>Previous page<');
    expect(markup).toContain('<option value="page" selected="">Whole page</option>');
    expect(markup).toContain('data-fit="page"');
    expect(markup).toContain('Full screen');
    expect(markup).toContain('title="Next page (→)"');
    expect(markup).toContain('<span class="reader-fullscreen-label">Full screen</span>');
    // The toolbar follows the page so keyboard order matches a toolbar placed under it.
    expect(markup.indexOf('data-testid="pdf-canvas"')).toBeLessThan(markup.indexOf('class="reader-controls"'));
  });
  it('can put document search behind a disclosure', () => {
    const backend: PrintBackend = { open: () => { throw new Error('Server rendering must not fetch a PDF'); } };
    const search = async () => [];
    const open = renderToStaticMarkup(<PrintReader url="/api/approved.pdf" backend={backend} title="Fixture edition" search={search} />);
    expect(open).toContain('Search this document');
    expect(open).not.toContain('reader-tool');
    const toggled = renderToStaticMarkup(<PrintReader url="/api/approved.pdf" backend={backend} title="Fixture edition" search={search} searchToggle />);
    expect(toggled).toMatch(/<button type="button" class="reader-tool" aria-expanded="false" aria-controls="reader-tool-panel"[^>]*><svg[^]*<span class="reader-tool-label">Search &amp; text<\/span><\/button>/);
    expect(toggled).not.toContain('<form');
    expect(toggled).not.toContain('<details');
  });
  it('fits the whole page into the box or scales from the width', () => {
    const page = { width: 600, height: 900 };
    expect(printPageScale('page', { width: 1200, height: 450 }, page)).toBe(0.5);
    expect(printPageScale('page', { width: 300, height: 4000 }, page)).toBe(0.5);
    expect(printPageScale('width', { width: 300, height: 0 }, page)).toBe(0.5);
    expect(printPageScale('2', { width: 300, height: 0 }, page)).toBe(1);
    expect(printPageScale('width', { width: 3000, height: 0 }, page)).toBe(1.5);
  });
});
