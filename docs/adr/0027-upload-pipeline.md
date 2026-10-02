# 0027: One upload pipeline

Status: Accepted. Refines the crash-window statement of ADR 0017.

Context: The sequence "stage pending row, upload, record message id,
publish, roll back on failure" was written three times: single-file cp, the
album lone-member send, and the album group send. ADR 0017 required album
crash windows to mirror the single-upload policy exactly, but the copies had
drifted. Album pending rows were staged before the destination locks were
taken, so a lock conflict (or a missing discussion group) left pending rows
that `td repair --pending` would later re-upload as loose files. After a
group reached Telegram, the error code depended on the failure class
(`ERR_DB` or RPC meant "orphaned"), not on whether anything was actually
orphaned. A failed first group left the pending rows of later groups
behind. Thumbnails were dropped for photos only on the album path. The
threads default, resumable keys, and thumbnail reading were duplicated.

Decision: Single-file cp, multi-file and recursive cp, and imports go
through one in-package pipeline (`runUpload`). It takes planned members
(local source, requested destination, presentation, human caption) plus the
conflict policy, and owns the whole sequence:

- Planning reads only: source checks, conflict policy, and file/dir
  invariants run for every member before anything is written.
- All destination locks are taken together. Under them the pipeline checks
  the discussion group, reads thumbnails, and then hashes, resolves the
  pending row (adopt, supersede under `--replace`, or refuse) and stages
  each member. A strict run that hits a refusal discards the rows it staged
  and sends nothing, as ADR 0017 requires.
- Members are sent in units: a lone member as an ordinary message with its
  own manifest reply, otherwise a media group of up to ten members of one
  kind with one `td-album:v1` inventory. A `--replace` target is retired
  after its unit publishes. Replace is single-file only, so it never shares
  a group.
- One crash-window policy covers every unit. A member that never reached
  Telegram loses its pending row unless it is a resumable big file, and this
  includes members of later units that were never attempted. Once a unit
  reached Telegram, any failure to record, write the inventory, or publish
  rolls back the whole unit. The command reports `ERR_ORPHANED_UPLOAD`
  exactly when some media could not be deleted and stays orphaned.
  Otherwise it reports the underlying error. This is the single-upload rule,
  applied per unit.
- Thumbnails follow each member's own presentation. Photos taking no
  thumbnail is a presentation rule enforced once by `Presentation.Validate`
  (and ignored by the adapters), not a difference between paths.

Consequences: The three copies of the upload sequence are now one. Album runs
no longer leave pending rows after lock conflicts, a missing discussion
group, or a failed earlier group. An album whose rollback succeeded now
fails with the underlying code (for example `ERR_DB`) instead of
`ERR_ORPHANED_UPLOAD`. An album whose inventory could not be written and
whose media could not be deleted now reports `ERR_ORPHANED_UPLOAD`. Error
wording for a lone album member that is stranded now matches single-file
cp. JSON envelopes and CLI contracts are unchanged.
