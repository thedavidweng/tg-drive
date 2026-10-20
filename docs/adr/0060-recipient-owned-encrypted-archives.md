# 0060: Recipient-owned age archives and verified atomic restore

## Status

Accepted

## Context

An ordinary recoverable drive exposes names and metadata to Telegram. A private
archive must encrypt its manifest and filenames as well as file bytes, and a
failed restore must not leave an apparently complete destination or overwrite
an existing directory. Inventing an encryption container would add unnecessary
cryptographic and compatibility risk.

## Decision

Use filippo.io/age with an explicitly supplied X25519 public recipient. An
exclusive keygen command creates an owner-private identity file and prints only
its public recipient. Restore refuses shared or nonregular identity files. Key
custody belongs to the user; Telegram and the index do not hold private keys.

Encrypt a tar stream containing `.td-archive-v1.json` followed by its ordered
entries. Manifest version 1 records portable relative paths, directory flags,
lengths and BLAKE3 hashes; the manifest is bounded to 8 MiB. Names and manifest
stay inside age ciphertext. Pack rejects symlinks/special files and verifies
stable source bytes. Upload ciphertext through the existing immutable-file
pipeline, preserving normal metadata reconstruction of the encrypted object.

Restore into a private staging directory in the destination's parent. Reject
unsupported versions, nonportable/traversing/duplicate paths, links, special
entries, mismatched order/size/hash and trailing payload. Consume the entire age
reader after tar EOF so final-chunk authentication is required. Publish only
after all verification and cancellation checks, using native exclusive rename:
RENAME_NOREPLACE on Linux, RENAME_EXCL on macOS, and MoveFile on Windows. This
also refuses an existing empty directory atomically, without a check-then-rename
race or a copying substitute. Failed staging is removed.

## Consequences

Index loss can be repaired with the existing full scan; a restored encrypted
object still requires its separately owned matching private identity. Public
recipient encryption provides ciphertext integrity and confidentiality, not
sender identity authentication. Randomized ciphertext does not deduplicate
plaintext across encrypted archives. Restored directories/files use private
permissions; ownership, ACLs, executable bits and timestamps are not preserved.
No key escrow, automatic key recovery, writable encrypted mount or custom
cryptographic framing is introduced.
