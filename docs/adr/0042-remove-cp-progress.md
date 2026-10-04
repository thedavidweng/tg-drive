# 0042: Remove cp.progress in favour of Transfer events

Status: Accepted. Supersedes in part ADR 0005 (the `cp.progress` event).

Context: ADR 0005 gave `td cp --events` a `cp.progress` line per confirmed
upload part, its data the Telegram client's `UploadProgressState`. ADR 0033
then made every `td cp` form run as a Transfer, and single-file uploads and
`td transfers retry --events` added `transfer.stage` lines next to it. A
consumer reading `--events` got two overlapping vocabularies: stage changes
named the Transfer, part reports did not, and the multi-file and
`--recursive` forms reported parts only. `transfer.stage` alone could not
replace `cp.progress`: it fires on stage changes, so between `uploading` and
`publishing` it carried no byte progress and no part detail.

Decision: The Transfer Manager's events are the one `--events` vocabulary
for uploads, and `cp.progress` is removed.

- A new `transfer.progress` event reports each confirmed upload part,
  unthrottled. Its data is the Transfer, the same shape `transfer.stage` and
  `td transfers show` use, plus `part`: `file_name`, `index`, `size`,
  `uploaded`, and `total`. Every field `cp.progress` carried survives, so
  the Transfer events are a strict superset of what they replace.
- Progress stays a separate event name rather than more `transfer.stage`
  lines, so `transfer.stage` keeps meaning "the Transfer entered a stage".
- Every `td cp --events` form (single file, album, `--recursive`) and
  `td transfers retry --events` emit `transfer.stage` and
  `transfer.progress`; `cp.progress` is no longer emitted by any command.
- Removing an event is a breaking change to the JSON contract, so
  `meta.schema_version` moves from `2026-07-29` to `2026-10-03`. The
  removal is made at once rather than through a deprecation window: the
  project is pre-1.0, and keeping both events would keep the duplication
  this decision removes.

Consequences:

- Scripts matching `meta.command == "cp.progress"` stop seeing progress and
  must match `transfer.progress`, reading `data.part.index` for the old
  `part` and `data.part.size` for `part_size`. They can check
  `meta.schema_version` to detect the change.
- Every progress line names its Transfer (`data.id`), so a consumer can
  correlate it with `td transfers` and `td transfers watch` without
  guessing from the file name.
- Each part line carries a whole Transfer, so the stream is larger per part
  than before. Part counts are bounded by file size over part size, so this
  stays small next to the bytes uploaded.
- The Transfer fields in a `transfer.progress` line are the in-memory
  snapshot; the index still records byte progress at most every 250 ms, so
  `updated_at` can lag the part it accompanies.
