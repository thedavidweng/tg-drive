# 0039: Repair pre-release Transfer schema collisions in place

Status: Accepted.

Context: Earlier local databases had already recorded schema versions 2 and
3 before those numbers were reused for the Transfer table and its failed-item
column. Opening one with the current code skipped both migrations: the
database reported version 3 but had no `transfers` table. The pre-release
squash policy treated old databases as disposable, but a local index may
contain channel bindings and files that users should not have to recreate
just to open the GUI's Transfers tab.

Decision: A new version-4 migration checks the actual Transfer schema and
adds the table and failed-item column only if missing. Version 3 first
ensures the table exists for databases that previously recorded version 2.
The schema repair and version record share a transaction. The repair does
not reset the index, bound channels, or existing Transfer records.

Consequences: Both old and current version-2/3 databases converge on the
same schema without deleting user data. A version number alone is not
enough to infer that a pre-release Transfer migration ran; the repair
checks the schema explicitly. Tests reopen representative databases twice
to verify preservation and idempotence.
