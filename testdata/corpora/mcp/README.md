# testdata/corpora/mcp

Seed corpus for `FuzzServeLine` (`internal/mcp/fuzz_test.go`), plus one hand-drive script.

Every file whose name is not listed under "Not seeds" below is fed to the fuzzer verbatim, as
one request line. The shapes are the ones the JSON-RPC framing layer has to survive rather than
the ones it has to understand:

| File | Shape |
|---|---|
| `initialize.json` | a valid `initialize` request — the handshake a real host opens with |
| `notification.json` | a notification: no `id`, so no response may be written |
| `truncated_object.json` | an object cut off mid-string, the shape a killed writer leaves behind |
| `embedded_nul.bin` | a NUL inside a JSON string, which is legal bytes and illegal JSON |
| `wrong_version.json` | `{"jsonrpc":"1.0"}` — parses, but is not this protocol |

## The 2 MiB frame is generated, not committed

The sixth seed — one line of 2 MiB, twice `defaultMaxLine` — is added by `f.Add` in
`fuzz_test.go` rather than checked in here. What it exercises is a property of its LENGTH (the
accepted-line ceiling, and the stream resynchronizing after a frame is refused for it), and a
generator states that in one line where a 2 MiB blob would state it in two million. The blob
would also be reviewed once and never again, and would cost every clone of this repository that
much forever.

## Not seeds

- `README.md` — this file.
- `initialize.ndjson` — a three-line hand-drive script (`initialize`,
  `notifications/initialized`, `tools/list`) for piping into `qompack mcp` as a smoke check. It
  is newline-framed, so it is a conversation rather than a single request line, which is why the
  fuzz target skips it.
