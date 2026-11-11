# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`aegis-mcp` is a local **stdio MCP server** in Go that lets an AI client read and change an Aegis organisation's managed files (the `content` and `config` planes) through the Aegis admin API, so customers don't have to edit them in the admin UI. One static binary per platform, published as GitHub releases from `v*` tags.

Aegis itself (Flask API + React UI + Go CLI) is a separate, private codebase. Its own `CLAUDE.md` is the authority on API behaviour. Read the relevant section there before adding or changing a tool. Where to find it is in `CLAUDE.local.md` (git-ignored), never in a tracked file.

## This repository is public

The source is open on GitHub (`Krathos-systems/aegis-mcp`). Aegis's rule that **nothing a customer can see mentions how the service is run** applies to every file here, every commit message and every release note:

- Never name the private forge, internal hosts, server names, private IPs, local or build-machine paths, or the private repo. Private pointers go in `CLAUDE.local.md`, which is git-ignored.
- Never say "self-hosted", "this instance", `.env`, or name a server-side environment variable.
- `TestNoInternalWordsInWhatClientsSee` checks the instructions and tool descriptions. The release workflow's `scripts/leak-check.sh` greps the binaries and notes for patterns kept in the `LEAK_PATTERNS` repo secret, because the patterns are private and cannot be committed. CI runs it over every tracked file, and a release fails if the secret is unset.

## Commands

Everything runs in `golang:1.26.3` through the Makefile, with caches in `.gocache/` (git-ignored):

```bash
make check                    # gofmt + vet + race tests + 100% coverage gate (what CI runs)
make test TEST=TestSession    # one test or a regex
make cover-html               # .gocache/cover.html, after make cover
make build                    # ./aegis-mcp (linux/amd64)
make dist VERSION=v0.1.0      # all five targets + SHA256SUMS into dist/
make tidy / make fmt-fix
```

Keep `GO_IMAGE` in the Makefile and the `go` directive in `go.mod` in step. CI reads the Go version from `go.mod`.

## Architecture

```
main.go               flags + config, builds the mcp.Server, runs it over stdio
internal/config       env → Config (AEGIS_SERVER, AEGIS_TOKEN | AEGIS_TOKEN_FILE, AEGIS_READ_ONLY)
internal/aegis        the only HTTP code: Client.Do, StatusError, file/admin endpoint wrappers
internal/tools        MCP tool definitions, handlers, explain(), and the Instructions string
```

- **`main.run` takes its args, stdio and `getenv` as parameters** so tests drive the real program in-process (`TestSession`). `main()` only wires `os.*`. `TestMainProcess` re-runs the test binary as a child to cover it.
- **stdout is the protocol.** Anything for a person goes to stderr. A stray `fmt.Println` breaks every client.
- **Auth is `Authorization: Bearer <token>`** on every `/api` call. `/health` is called without it. The client never follows redirects, so the token can't be carried elsewhere. The token is never put in an error, a log line or argv.
- **Aegis is the permission boundary.** Tools don't pre-check levels; they call the API and turn the refusal into advice through `tools.explain` (403 `required`/`held`, 409 "read it again", 402/413 limits, `org_state` on 410/423/503). Keep the server's own `error` sentence in the message.
- **Planes and paths.** `content` = Markdown/skills/agents, `config` = `settings.json`/`mcp.json`, each laid out by layer: `global`, `teams/<t>`, `roles/<r>`, `repos/<repo>`, `repos/<repo>/roles/<r>`, each then `claude/…`. `aegis.CleanPath` rejects absolute, `..`, empty and backslash segments and URL-escapes each segment. The server checks containment too.
- **Optimistic concurrency is mandatory for changes.** Aegis honours `If-Match` but treats a missing one as unconditional. The MCP **always** requires the etag for update and delete, so a model can never blind-overwrite another admin's edit.
- **Create vs update differ per plane on the server.** Content has `POST` (create, 409 if present) and `PUT` (update, 404 if missing). Config has only `PUT` (create-or-overwrite). `aegis.CreateFile` on config does a `GET` first and refuses with `ErrExists`. That leaves a small race window, accepted for the alpha. `update` reports an error if a config `PUT` unexpectedly created the file.
- **Read-only mode** (`--read-only` / `AEGIS_READ_ONLY`) leaves the write tools out of `tools/list` entirely rather than refusing at call time.
- **Tool inputs** are typed structs. The SDK infers and validates the JSON schema. `planeEnum` adds the `content|config` enum, and handlers still call `aegis.ParsePlane` as a second guard. A new tool goes in `tools.Register` (inside the `ReadOnly` early return if it writes), with `readOnly(...)` annotations or `DestructiveHint` set honestly.
- **`tools.Instructions`** is the model's map of Aegis. If Aegis's layout, waterfall or variable rules change, update it. A wrong rule there produces wrong edits.

## Dependency on Aegis server work

Aegis admin routes currently authenticate only browser sessions (`POST /admin/login` → 15-min JWT + refresh cookie, MFA if enabled). Delivery API keys (`X-API-Key`) can't reach admin routes. The MCP is built against **admin API tokens sent as Bearer**, which still have to be added to Aegis. Until then, a session access token from `/admin/login` works as `AEGIS_TOKEN` for manual testing because Aegis reads JWTs from the same header. It expires in 15 minutes.

## Testing

`make check` enforces **100% statement coverage of every function** (`scripts/coverage.sh`). A new branch needs a test. If a branch genuinely can't be reached, remove it rather than excluding it.

- `internal/aegis`: `httptest` servers per case (`serve`, `reply`, `closedServer`).
- `internal/tools`: end-to-end through the SDK's in-memory transports. `connect` uses `fakeAegis` (stateful files + ETags + If-Match). `connectTo` takes any handler, e.g. `routes{"GET /api/repos": {200, "…"}}` or `failing(403, …)`.
- `main_test.go`: the real program over `io.Pipe` (`TestSession`), and the child-process `TestMainProcess`.

## Releases

Versioned by git tag, like Aegis. Alpha tags look like `v0.1.0-alpha.1`. Any tag with a hyphen is published as a GitHub pre-release.

1. Add a `## vX.Y.Z — <date>` section to `CHANGELOG.md`, written for customers. `scripts/release-notes.sh <tag>` extracts it, and the workflow stops if it's missing.
2. Tag and push. `.github/workflows/release.yml` runs the checks, builds `aegis-mcp-<os>-<arch>[.exe]` for linux/darwin amd64+arm64 and windows/amd64, writes `SHA256SUMS`, runs the leak check, then creates the release (re-running for an existing tag replaces its assets and notes).

Releases are not signed yet. Aegis signs its CLI releases with an Ed25519 key (`SHA256SUMS.sig`), and the same scheme should come here before 1.0.
