# 0042: NDJSON event stream for td scan

Status: Accepted. Extends ADR 0005 to `td scan`.

Context: `td scan --full` on a large channel runs for minutes, and its only
progress was human text on stderr. ADR 0005 gave `td cp` an NDJSON stream;
scripts and agents need the same for a scan. The scan use case already
reports its stages and per-message results through the service Observer
(ADR 0032), which td-gui folds into its scan-progress events.

Decision: `td scan --events` writes the scan's Observer reports through
`Renderer.Event`, one envelope per line on stdout, with or without
`--json`.

- `scan.stage` (`{"stage": "reading" | "indexing"}`) when the scan starts
  reading Telegram history and when it starts rebuilding the index.
- `scan.item` for each message the scan accounts for: a `completed` item
  for each file indexed, a `failed` item with the recorded scan error's
  `code` and `message` for each scan error. Each line carries the running
  `indexed` and `failed` tallies, so a consumer tracks progress from the
  last line alone.
- A final `scan` line carrying the normal `td scan` result; an error ends
  the stream with the usual error envelope.
- Every line shares the invocation's `meta.request_id`.

Consequences: Consumers track a scan without parsing stderr. The reading
stage reports no per-message progress, so a long history read shows as one
`scan.stage` line until indexing starts. `td cp --events` is unchanged.
