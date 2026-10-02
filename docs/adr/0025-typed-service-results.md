# 0025: Typed Service Results

Status: Accepted

Context: Service use cases returned `map[string]any` JSON payloads. Commands and internal callers (import, recursive upload) read them back through type assertions such as `data["message_id"].(int)`. A renamed key or an `int`/`int64` change failed silently and produced zero values. Album planning reported lenient member failures as `"path: error"` strings, and import recovered the failing member by prefix-matching the path.

Decision: Each service use case returns a named result struct declared next to its function. JSON tags reproduce the envelope `data` exactly. Fields are declared in alphabetical JSON-key order so the output stays byte-identical to the sorted keys that maps produced. Optional keys use `omitempty` when a present value is never zero. They use a pointer when zero is a valid present value, and `omitzero` for maps and slices that are absent when nil. One result shape that cannot be expressed through tags, a skipped single-file upload, uses a small `MarshalJSON`. Album planning returns typed per-member failures (local path plus error). Their rendered string is unchanged. Human `key: value` output reflects over the result's JSON field names.

Consequences: Consumers read fields that the compiler checks. JSON key order and presence are now encoded in struct declarations: a new field must be inserted in alphabetical position, or envelope byte order changes. The JSON contract is unchanged.
