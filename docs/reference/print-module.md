# Shared print module

The print module adapts the GPL-3.0-only Arkhiv reader, extraction pipeline and
synthetic PDF fixture from donor revision
`99d499b92cb8d848e0fcb37288ece8bce9de943e`. Source comments retain that provenance.
The shared viewer's `0.2.0` source version remains unreleased.

Consumers import `PrintReader` from `@subcult/archive-viewer/print` and supply a
PDF backend. Each page supplies width, height, cancellable canvas rendering and
readable text. The consumer owns its PDF library, worker asset, layout and CSS.
The root entry keeps existing video imports compatible without a PDF dependency.
Search appears only when the consumer supplies an actual search endpoint.
From viewer `0.4.9`, the optional `pageArrows`, `fitPage`, `fullscreen` and
`searchToggle` props add page arrows with keyboard and swipe turning, a
"Whole page" fit, full screen and a search-and-text panel. The reader exposes
`--print-page-ratio` and `--print-page-top` for the consumer's fitting CSS.
From `0.4.10` the page field commits on Enter or blur, a PDF that cannot
be opened shows only the error, and the overlay full screen marks the rest of
the page `inert`.

`vhness index-print` indexes an already preserved, reviewed PDF. `VHNESS_MEDIA`
contains immutable originals; `VHNESS_DERIVATIVES` is a separate directory for
generated covers. The server can mount both directories read-only after indexing.
Poppler reads the verified original through an inherited file descriptor, with
time, memory and output limits. Native text retains page numbers. An explicitly
reviewed OCR companion supplies document text with page zero when its page mapping
is unknown. When the companion separates pages with form feeds (as Tesseract and
OCRmyPDF sidecars do) and the count matches the PDF's pages, each page keeps its
number and the index reports `page-text`. Search snippets are centred on the
matching words.

`vhness rebind-representation --id ITEM --representation REP --collection C
--file input.json --notes ... [--approve-public]` moves an existing
representation to a newer snapshot of the same source, for example one that
adds a derived OCR companion, and applies the given transport review in the same
transaction. `input.json` holds `representation`, `review` and `preservation`.
The source, media and primary file cannot change and no companion may be
dropped. Reviews of the representation in other collections bind the old
snapshot and stop authorizing access until they are reviewed again. Run
`index-print` afterwards; until then the previous index is not served because its
snapshot no longer matches. A failed cover does not invalidate the original or extracted text.

Text and cover responses require the current reading review, source snapshot and
original hash. Revoked access or a corrupt original denies cached derivatives.
Covers have their own recorded hash and size. Reading permission does not grant
download permission.

Disposable integration tests extract a generated two-page PDF, render its cover,
exercise OCR fallback and deny revoked or corrupt originals. Package checks
install and import the print entry in a fresh consumer without a PDF library.
These checks do not establish real collection reading, browser rendering,
migration or independent recovery; those require separate qualification.
