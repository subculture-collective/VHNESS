// Adapted from Arkhiv Reader.tsx at 99d499b92cb8d848e0fcb37288ece8bce9de943e.
// GPL-3.0-only. PDF runtime/assets and page layout remain consumer-owned.
import { useEffect, useRef, useState, type CSSProperties, type FormEvent, type PointerEvent } from 'react';

export type PrintHit = { page: number; snippet: string };
export interface PrintPage {
  width: number;
  height: number;
  render(canvas: HTMLCanvasElement, scale: number): { promise: Promise<void>; cancel(): void };
  text(): Promise<string>;
}
export interface PrintDocument {
  numPages: number;
  page(number: number): Promise<PrintPage>;
}
export interface PrintBackend {
  open(url: string): { promise: Promise<PrintDocument>; destroy(): Promise<void> };
}
export type PrintReaderProps = {
  url: string;
  backend: PrintBackend;
  title: string;
  initialPage?: number;
  onPageChange?: (page: number) => void;
  search?: (query: string, signal: AbortSignal) => Promise<PrintHit[]>;
  /** Previous/next arrows beside the page instead of in the toolbar, plus arrow-key and swipe page turning.
   * This reading layout also leaves out the readable-text disclosure on pages without a text layer. */
  pageArrows?: boolean;
  /** Adds a "Whole page" zoom and starts with it. The page fits the `.canvas-wrap` box, so give it a height in CSS;
   * the reader sets `--print-page-ratio` (height / width of the current page) to help size it. */
  fitPage?: boolean;
  /** Adds a full-screen toggle for the reader; the F key also toggles it. */
  fullscreen?: boolean;
  /** Opens document search and the page's readable text from one "Text" toolbar button, as a panel under the page,
   * instead of an always-open search form and a readable-text disclosure. With page arrows, the toolbar also
   * follows the page in the markup so keyboard order matches a toolbar placed under it. */
  searchToggle?: boolean;
};

type Zoom = 'page' | 'width' | '1.25' | '1.5' | '2';

export function initialPrintPage(value: number | undefined, pages?: number): number {
  const page = value !== undefined && Number.isInteger(value) && value > 0 ? value : 1;
  return pages ? Math.min(page, pages) : page;
}

/** Scale that fits a page of this size into the box, for the chosen zoom. */
export function printPageScale(zoom: Zoom, box: { width: number; height: number }, page: { width: number; height: number }): number {
  if (zoom === 'page') return Math.min(box.width / page.width, box.height / page.height, 3);
  return Math.min(box.width / page.width, 1.5) * (zoom === 'width' ? 1 : Number(zoom));
}

function typingTarget(target: EventTarget | null) {
  return target instanceof HTMLElement && (target.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName));
}

export function PrintReader(props: PrintReaderProps) {
  return <ReaderSession key={props.url} {...props} />;
}

function Arrow({ direction }: { direction: 'previous' | 'next' }) {
  return <svg viewBox="0 0 24 24" width="28" height="28" aria-hidden="true" focusable="false">
    <path d={direction === 'previous' ? 'M15 4 7 12l8 8' : 'm9 4 8 8-8 8'} fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="square" />
  </svg>;
}

function FullscreenIcon({ exit }: { exit: boolean }) {
  return <svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true" focusable="false">
    <path d={exit ? 'M9 4v5H4M15 4v5h5M9 20v-5H4M15 20v-5h5' : 'M3 9V3h6M21 9V3h-6M3 15v6h6M21 15v6h-6'} fill="none" stroke="currentColor" strokeWidth="2" />
  </svg>;
}

function SearchIcon() {
  return <svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true" focusable="false">
    <circle cx="10.5" cy="10.5" r="6" fill="none" stroke="currentColor" strokeWidth="2.2" /><path d="m15 15 5.5 5.5" stroke="currentColor" strokeWidth="2.2" />
  </svg>;
}

function ReaderSession({ url, backend, title, initialPage, onPageChange, search, pageArrows, fitPage, fullscreen, searchToggle }: PrintReaderProps) {
  const [document, setDocument] = useState<PrintDocument | null>(null);
  const [page, setPage] = useState(initialPrintPage(initialPage));
  const [zoom, setZoom] = useState<Zoom>(fitPage ? 'page' : 'width');
  const [box, setBox] = useState({ width: 700, height: 900 });
  const [isFullscreen, setIsFullscreen] = useState(false);
  // Where the Fullscreen API is missing or refused (iPhone Safari), the reader becomes a fixed overlay.
  const [overlay, setOverlay] = useState(false);
  const [pageRatio, setPageRatio] = useState<number | null>(null);
  // Distance from the top of the document to the page box, so CSS can fit the page into the rest of the screen.
  const [pageTop, setPageTop] = useState<number | null>(null);
  const swipe = useRef<{ id: number; x: number; y: number } | null>(null);
  const [toolOpen, setToolOpen] = useState(false);
  const [text, setText] = useState('');
  // Whether the last rendered page had a text layer; kept while the next page loads so the control does not flicker.
  const [hasText, setHasText] = useState(true);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);
  const [query, setQuery] = useState('');
  const [searchResult, setSearchResult] = useState<{ query: string; hits?: PrintHit[]; error?: string; pending?: boolean } | null>(null);
  const canvas = useRef<HTMLCanvasElement>(null);
  const wrapper = useRef<HTMLDivElement>(null);
  const row = useRef<HTMLDivElement>(null);
  const section = useRef<HTMLElement>(null);
  const searchController = useRef<AbortController | null>(null);
  const expanded = isFullscreen || overlay;
  const effectiveZoom: Zoom = zoom;
  // Only the whole-page fit depends on the box height; at other zooms the box grows with the page.
  const fitWidth = box.width;
  const fitHeight = effectiveZoom === 'page' ? box.height : 0;
  useEffect(() => () => searchController.current?.abort(), []);
  useEffect(() => {
    const pageRow = row.current, pageBox = wrapper.current;
    if (!pageRow || !pageBox) return;
    // Width comes from the row minus the arrows, so the page box itself may shrink to
    // the page and keep the arrows beside it; height comes from the page box (whole-page fit).
    const measure = () => {
      const style = getComputedStyle(pageRow);
      // Arrows placed over the page (position: absolute) take no width from it.
      const arrows = ([...pageRow.children].filter(child => child !== pageBox) as HTMLElement[])
        .filter(arrow => getComputedStyle(arrow).position !== 'absolute');
      const gap = parseFloat(style.columnGap) || 0;
      const used = arrows.reduce((total, arrow) => total + arrow.offsetWidth + gap, 0);
      const width = Math.max(1, Math.round(pageRow.clientWidth - (parseFloat(style.paddingLeft) || 0) - (parseFloat(style.paddingRight) || 0) - used));
      const height = Math.max(1, Math.round(pageBox.clientHeight));
      setBox(previous => previous.width === width && previous.height === height ? previous : { width, height });
      // Full screen moves the reader to the top of the screen; keep the in-page position for when it returns.
      if (!section.current?.hasAttribute('data-fullscreen')) setPageTop(Math.round(pageBox.getBoundingClientRect().top + globalThis.scrollY));
    };
    const observer = new ResizeObserver(measure);
    observer.observe(pageRow);
    observer.observe(pageBox);
    // Content above the reader (a wrapping title, a notice) moves the page box without resizing it.
    observer.observe(globalThis.document.body);
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    if (!fullscreen) return;
    const changed = () => setIsFullscreen(globalThis.document.fullscreenElement === section.current);
    globalThis.document.addEventListener('fullscreenchange', changed);
    return () => globalThis.document.removeEventListener('fullscreenchange', changed);
  }, [fullscreen]);
  useEffect(() => {
    let active = true;
    let task: ReturnType<PrintBackend['open']> | undefined;
    try {
      task = backend.open(url);
      void task.promise.then(pdf => {
        if (!active) return;
        if (!Number.isInteger(pdf.numPages) || pdf.numPages < 1 || pdf.numPages > 10000) throw new Error('Invalid page count');
        setDocument(pdf); setPage(value => initialPrintPage(value, pdf.numPages));
      }).catch(() => {
        if (active) { setError('The PDF could not be opened. Try again or open its source page.'); setLoading(false); }
      });
    } catch {
      setError('The PDF could not be opened. Try again or open its source page.'); setLoading(false);
    }
    return () => { active = false; if (task) void task.destroy().catch(() => undefined); };
  }, [url, backend]);
  useEffect(() => {
    if (!document || !canvas.current) return;
    let cancelled = false;
    let render: ReturnType<PrintPage['render']> | undefined;
    setLoading(true); setText(''); setError('');
    void (async () => {
      const pdfPage = await document.page(page);
      if (cancelled || !canvas.current) return;
      if (!Number.isFinite(pdfPage.width) || !Number.isFinite(pdfPage.height) || pdfPage.width <= 0 || pdfPage.height <= 0) throw new Error('Invalid page dimensions');
      setPageRatio(Math.round((pdfPage.height / pdfPage.width) * 1000) / 1000);
      // Draw at the screen's pixel density so scans stay sharp, within a bounded canvas area.
      const cssScale = printPageScale(effectiveZoom, { width: fitWidth, height: fitHeight }, pdfPage);
      const density = Math.min(globalThis.devicePixelRatio || 1, 3);
      const renderScale = Math.min(cssScale * density, Math.sqrt(16000000 / (pdfPage.width * pdfPage.height)));
      render = pdfPage.render(canvas.current, renderScale);
      canvas.current.style.width = `${Math.round(pdfPage.width * cssScale)}px`;
      canvas.current.style.height = `${Math.round(pdfPage.height * cssScale)}px`;
      await render.promise;
      if (cancelled) return;
      const readable = await pdfPage.text();
      if (!cancelled) { setText(readable); setHasText(Boolean(readable.trim())); setLoading(false); }
    })().catch(() => { if (!cancelled) { setError('This page could not be rendered.'); setLoading(false); } });
    return () => { cancelled = true; render?.cancel(); };
  }, [document, page, fitWidth, fitHeight, effectiveZoom]);
  function go(next: number) {
    if (document && Number.isInteger(next) && next >= 1 && next <= document.numPages) {
      setPage(next); onPageChange?.(next);
      // A zoomed page starts at its top; bring the page back into view if the reader scrolled past it.
      wrapper.current?.scrollTo?.({ top: 0, left: 0 });
      if (row.current && row.current.getBoundingClientRect().top < 0) row.current.scrollIntoView?.({ block: 'start' });
    }
  }
  const turn = useRef(go);
  turn.current = go;
  const current = useRef(page);
  current.current = page;
  useEffect(() => {
    if (!pageArrows) return;
    const key = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey || typingTarget(event.target)) return;
      const sideways = event.key === 'ArrowLeft' || event.key === 'ArrowRight';
      // Left and right pan a zoomed page that is wider than its box; Page Up/Down still turn pages.
      if (sideways && wrapper.current && wrapper.current.scrollWidth > wrapper.current.clientWidth + 1) {
        event.preventDefault();
        wrapper.current.scrollBy?.({ left: event.key === 'ArrowLeft' ? -120 : 120 });
        return;
      }
      const step = event.key === 'ArrowLeft' || event.key === 'PageUp' ? -1 : event.key === 'ArrowRight' || event.key === 'PageDown' ? 1 : 0;
      if (step) { event.preventDefault(); turn.current(current.current + step); }
    };
    globalThis.addEventListener('keydown', key);
    return () => globalThis.removeEventListener('keydown', key);
  }, [pageArrows]);
  const toggle = useRef(toggleFullscreen);
  toggle.current = toggleFullscreen;
  useEffect(() => {
    if (!fullscreen) return;
    const key = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey || typingTarget(event.target)) return;
      if (event.key === 'f' || event.key === 'F') { event.preventDefault(); toggle.current(); }
    };
    globalThis.addEventListener('keydown', key);
    return () => globalThis.removeEventListener('keydown', key);
  }, [fullscreen]);
  useEffect(() => {
    if (!overlay) return;
    const close = (event: KeyboardEvent) => { if (event.key === 'Escape') setOverlay(false); };
    globalThis.addEventListener('keydown', close);
    return () => globalThis.removeEventListener('keydown', close);
  }, [overlay]);
  function toggleFullscreen() {
    if (overlay) { setOverlay(false); return; }
    if (globalThis.document.fullscreenElement) { void globalThis.document.exitFullscreen().catch(() => undefined); return; }
    if (fitPage) setZoom('page');
    const request = section.current?.requestFullscreen?.bind(section.current);
    if (request) void request().catch(() => setOverlay(true));
    else setOverlay(true);
  }
  async function find(event: FormEvent) {
    event.preventDefault();
    if (!search || !query.trim()) return;
    searchController.current?.abort();
    const controller = new AbortController(); searchController.current = controller;
    const requested = query;
    setSearchResult({ query: requested, pending: true });
    try {
      const hits = await search(requested, controller.signal);
      if (!controller.signal.aborted) setSearchResult({ query: requested, hits });
    } catch {
      if (!controller.signal.aborted) setSearchResult({ query: requested, error: 'Document search is unavailable. Please try again.' });
    }
  }
  const result = searchResult?.query === query ? searchResult : null;
  // Swipe left or right on a touch screen to turn the page, when the whole page is in view. The page box
  // keeps vertical scrolling and pinch zoom but leaves sideways gestures to the reader, so the browser does
  // not take a swipe as a pan and cancel the pointer.
  const swipeable = pageArrows && effectiveZoom === 'page';
  function swipeStart(event: PointerEvent) {
    if (swipeable && event.pointerType !== 'mouse') swipe.current = { id: event.pointerId, x: event.clientX, y: event.clientY };
  }
  function swipeEnd(event: PointerEvent) {
    const start = swipe.current;
    swipe.current = null;
    if (!start || start.id !== event.pointerId) return;
    const dx = event.clientX - start.x, dy = event.clientY - start.y;
    if (Math.abs(dx) > 50 && Math.abs(dx) > Math.abs(dy) * 1.5) go(page + (dx < 0 ? 1 : -1));
  }
  const searchForm = search && <form onSubmit={event => { void find(event); }} className="search">
    <label>Search this document <input type="search" value={query} onChange={event => setQuery(event.target.value)} maxLength={200} required /></label><button>Find</button>
  </form>;
  const searchResults = <>
    {result?.pending && <p role="status">Searching document text…</p>}
    {result?.error && <p role="alert">{result.error}</p>}
    {result?.hits && <div aria-live="polite"><p>{result.hits.length} matching pages or text sections</p>{result.hits.map((hit, index) => <p key={index}>
      {hit.page > 0 ? <button onClick={() => go(hit.page)}>Page {hit.page}</button> : 'Document text; page mapping unavailable'}: {hit.snippet}
    </p>)}</div>}
  </>;
  const style = { ...(pageRatio ? { '--print-page-ratio': pageRatio } : {}), ...(pageTop !== null ? { '--print-page-top': `${pageTop}px` } : {}) } as CSSProperties;
  const previous = <button className={pageArrows ? 'page-arrow page-arrow-previous' : undefined} aria-label={pageArrows ? 'Previous page' : undefined} title={pageArrows ? 'Previous page (←)' : undefined} onClick={() => go(page - 1)} disabled={!document || page <= 1}>{pageArrows ? <Arrow direction="previous" /> : 'Previous page'}</button>;
  const next = <button className={pageArrows ? 'page-arrow page-arrow-next' : undefined} aria-label={pageArrows ? 'Next page' : undefined} title={pageArrows ? 'Next page (→)' : undefined} onClick={() => go(page + 1)} disabled={!document || page >= document.numPages}>{pageArrows ? <Arrow direction="next" /> : 'Next page'}</button>;
  const controls = <div className="reader-controls">
      {fullscreen && expanded && <span className="reader-title">{title}</span>}
      {!pageArrows && previous}
      <span className="reader-pages">
        <label><span className="reader-label">Page</span> <input aria-label="Page number" type="number" min={1} max={document?.numPages || 1} value={page} onChange={event => go(Number(event.target.value))} /></label>
        <span className="reader-count">of {document?.numPages || '…'}</span>
      </span>
      {!pageArrows && next}
      <label><span className="reader-label">Zoom</span> <select aria-label="Zoom" value={zoom} onChange={event => setZoom(event.target.value as Zoom)}>
        {fitPage && <option value="page">Whole page</option>}
        <option value="width">Fit width</option><option value="1.25">125%</option><option value="1.5">150%</option><option value="2">200%</option>
      </select></label>
      {fullscreen && <button type="button" className="reader-fullscreen" aria-pressed={expanded} title={expanded ? 'Exit full screen (Esc)' : 'Full screen (F)'} onClick={toggleFullscreen}>
        <FullscreenIcon exit={expanded} /><span className="reader-fullscreen-label">{expanded ? 'Exit full screen' : 'Full screen'}</span>
      </button>}
      {searchToggle && (searchForm || hasText) && <button type="button" className="reader-tool" aria-expanded={toolOpen} aria-controls="reader-tool-panel"
        title="Search this document and read the page's text" onClick={() => setToolOpen(open => !open)}><SearchIcon /><span className="reader-tool-label">Search &amp; text</span></button>}
  </div>;
  return <section className="reader" aria-label="Document reader" aria-busy={loading} ref={section} data-fit={effectiveZoom === 'page' ? 'page' : 'width'} data-fullscreen={expanded || undefined}
    style={pageRatio || pageTop !== null ? style : undefined}>
    {!pageArrows && controls}
    {!searchToggle && searchForm}
    {!searchToggle && searchResults}
    {loading && <p role="status" className="reader-loading">Loading page…</p>}
    {error && <p role="alert">{error}</p>}
    <div className="reader-page" ref={row}>
      {pageArrows && previous}
      <div className="canvas-wrap" ref={wrapper} style={swipeable ? { touchAction: 'pan-y pinch-zoom' } : undefined} onPointerDown={swipeStart} onPointerUp={swipeEnd} onPointerCancel={() => { swipe.current = null; }}><canvas ref={canvas} aria-label={`${title}, PDF page ${page}`} data-testid="pdf-canvas" /></div>
      {pageArrows && next}
    </div>
    {pageArrows && controls}
    {searchToggle && toolOpen && <div className="reader-panel" id="reader-tool-panel">
      {searchForm}{searchResults}
      {hasText && <section className="reader-panel-text" aria-label="Readable text for this page"><h3>Text on this page</h3><p>{text || 'Loading page text…'}</p></section>}
    </div>}
    {!searchToggle && (hasText || !pageArrows) && <details className="reader-text"><summary>Readable text for this page</summary><p>{text || 'No text layer is available on this page.'}</p></details>}
  </section>;
}
