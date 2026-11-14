# Catalog labels

Core 0.4.6 adds typed labels for catalog items: `topic`, `place`, `organization`,
`person` and `event`. Migration 0025 adds `series` (see [Series](#series)). A label has a stable ID (`^[a-z0-9][a-z0-9._-]{0,99}$`), a
display label, aliases and a description. Assignments link an item to a label
with a state, a method, a score and evidence.

States are `proposed`, `accepted` and `rejected`. Only accepted assignments are
visible through the API, in public and private audiences alike. A proposal is a
suggestion from a method (a lexicon, a model, an editor) and stays private until
an operator accepts it. Every proposal and decision is recorded in
`catalog_label_history` with its notes.

Operator commands:

```sh
vhness import-labels --file vocabulary.json            # [{id, kind, label, aliases, description}]
vhness propose-labels --file proposals.json --notes …  # [{item, label, method, score, evidence, issue_position, issue_label}]
vhness review-labels --file decisions.json --notes …   # [{item, label, state}]
```

`import-labels` upserts the vocabulary; a label's kind cannot change while it is
assigned. `propose-labels` refreshes existing proposals but never overwrites an
accepted or rejected assignment, so a rerun cannot undo a review. Unknown items
or labels fail the whole batch. `review-labels` changes the state of existing
assignments, including moving one back to `proposed`.

HTTP:

- `GET /api/catalog/v1/labels?collection=&kind=` lists accepted labels on items
  visible in the audience scope, with item counts.
- `GET /api/catalog/v1/items?label=ID` filters by an accepted label, and items
  include their accepted `labels`.

## Series

Migration 0025 adds the `series` kind for ordered runs of a periodical. Each
assignment to a series label also carries an `issue_position` (an integer from
0, the reading order) and an `issue_label` (1 to 200 characters, such as
`Volume 3` or `42 (print)`). A series proposal must include both; a proposal for
any other kind must include neither. The batch fails otherwise. Review accepts
or rejects the proposal together with its issue fields, and history records them.

```json
[{"item": "riot-03", "label": "riot-grrrl", "method": "editor", "score": 1,
  "issue_position": 3, "issue_label": "Volume 3"}]
```

On items, a series label includes `issue_position` and `issue_label`.
`GET /api/catalog/v1/items?label=SERIES` returns the series' items in issue
order (ties fall back to newest date, then ID). Any other label filter keeps the
default newest-first order.

The print compatibility routes also serve series:

- `GET /api/documents?series=SLUG` returns only documents with that accepted
  series label, in issue order. A malformed slug returns 400.
- Every document carries `series`: `[{slug, name, issue_position, issue_label}]`,
  empty when it belongs to no series.
- `GET /api/facets` returns `{"subjects": [...], "series": [{slug, name}]}`,
  listing accepted series on published documents in the collection.

Evidence is stored for review and is not exposed by the API. Keep generated
proposals reproducible: record the method version and the inputs it read.
