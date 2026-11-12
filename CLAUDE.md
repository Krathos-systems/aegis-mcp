# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`aegis-mcp` is a local **stdio MCP server** in Go that lets an AI client read and change an Aegis organisation's managed files (the `content` and `config` planes) through the Aegis admin API, so customers don't have to edit them in the admin UI. One static binary per platform, published as GitHub releases from `v*` tags, plus installers and a container image.

**Installation must stay one command.** `install.sh` / `install.ps1` download and verify the binary, then run `aegis-mcp setup`, which asks two questions and registers the server with Claude Code. A change that adds a step for the user (a flag to pass, a file to edit, an env var to export) needs a very good reason.

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
make test-install             # install.sh against a fake release served from dist/ (needs python3)
make image                    # Docker image as aegis-mcp:dev
make tidy / make fmt-fix
```

Keep `GO_IMAGE` in the Makefile and the `go` directive in `go.mod` in step. CI reads the Go version from `go.mod`.

## Architecture

```
main.go               `aegis-mcp` (serve over stdio) and `aegis-mcp setup`
internal/config       env + config file → Config; NormalizeServer; the config file's read/write
internal/setup        `setup`: ask, check with GET /admin/me, save, register with Claude Code
internal/aegis        the only HTTP code: Client.Do, StatusError, file/admin endpoint wrappers
internal/tools        MCP tool definitions, handlers, explain(), the Instructions string,
                      compose.go (aegis_compose_context) and prompts.go (the MCP prompts)
install.sh/.ps1       one-line installers, published as release assets
Dockerfile, docker-compose.yml, aegis-mcp.env.example
```

- **`main.run` takes its args, stdio and `getenv` as parameters** so tests drive the real program in-process (`TestSession`). `main()` only wires `os.*`. `TestMainProcess` re-runs the test binary as a child to cover it.
- **stdout is the protocol.** Anything for a person goes to stderr. A stray `fmt.Println` breaks every client.
- **Auth is `Authorization: Bearer <token>`** on every `/api` call. `/health` is called without it. The client never follows redirects, so the token can't be carried elsewhere. The token is never put in an error, a log line or argv.
- **Aegis is the permission boundary.** Tools don't pre-check levels; they call the API and turn the refusal into advice through `tools.explain` (403 `required`/`held`, 409 "read it again", 402/413 limits, `org_state` on 410/423/503). Keep the server's own `error` sentence in the message.
- **Planes and paths.** `content` = Markdown/skills/agents, `config` = `settings.json`/`mcp.json`, each laid out by layer: `global`, `teams/<t>`, `roles/<r>`, `repos/<repo>`, `repos/<repo>/roles/<r>`, each then `claude/…`. `aegis.CleanPath` rejects absolute, `..`, empty and backslash segments and URL-escapes each segment. The server checks containment too.
- **Optimistic concurrency is mandatory for changes.** Aegis honours `If-Match` but treats a missing one as unconditional. The MCP **always** requires the etag for update and delete, so a model can never blind-overwrite another admin's edit.
- **Create vs update differ per plane on the server.** Content has `POST` (create, 409 if present) and `PUT` (update, 404 if missing). Config has only `PUT` (create-or-overwrite). `aegis.CreateFile` on config does a `GET` first and refuses with `ErrExists`. That leaves a small race window, accepted for the alpha. `update` reports an error if a config `PUT` unexpectedly created the file.
- **Read-only mode** (`--read-only` / `AEGIS_READ_ONLY`) leaves the write tools out of `tools/list` entirely rather than refusing at call time.
- **Tool inputs** are typed structs. The SDK infers and validates the JSON schema. `enum(...)` adds enums (`plane`, variable `kind`); optional fields are pointers or `omitempty`, and handlers still call `aegis.ParsePlane` as a second guard. A new tool goes in `tools.Register` (inside the `ReadOnly` early return if it writes), with `readOnly(...)` annotations or `DestructiveHint` set honestly.
- **Config precedence.** `AEGIS_SERVER` and `AEGIS_TOKEN`/`AEGIS_TOKEN_FILE` win. The config file (`<UserConfigDir>/aegis-mcp/config.json`, or `AEGIS_MCP_CONFIG`) fills in whatever the environment leaves out, and is read only then. Nothing configured gives `config.ErrNotConfigured`, whose message says to run `aegis-mcp setup`. The file holds the token, so `config.WriteFile` writes it `0600` via temp file + rename, and `ReadFile` warns when it is readable by others.
- **`setup` checks before it saves.** A token Aegis refuses is never written. It registers with Claude Code by running `claude mcp add --scope user aegis -- <absolute path of this binary>` when `claude` is on PATH. A failure there (most often "already exists") is explained and never fails setup. The token prompt uses `term.ReadPassword` on a terminal and a plain line otherwise. `--token-stdin` is the scripted form, and the installers use it when `AEGIS_SERVER` and `AEGIS_TOKEN` are set.
- **Installers.** `install.sh` is POSIX `sh` (macOS + Linux, curl or wget, sha256sum or shasum) and installs to `~/.local/bin` without sudo. It reads setup's answers from `/dev/tty`, because under `curl | sh` stdin is the script. `install.ps1` installs to `%LOCALAPPDATA%\Programs\aegis-mcp` and adds that to the user PATH. Both refuse a download whose checksum doesn't match. `AEGIS_MCP_RELEASES_URL` points them at another release root; that is how the tests serve a fake release.
- **Docker.** `FROM scratch`: the static binary, CA certificates, UID 65532. It must run with stdin attached and **no TTY** (`docker run -i`, `compose run -T`), or the TTY mangles the JSON stream. Compose reads `aegis-mcp.env` (git-ignored) and maps `host.docker.internal` to the host.
- **`tools.Instructions`** is the model's map of Aegis, and its placement ladder decides which layer a rule goes in. If Aegis's layout, waterfall or variable rules change, update it. A wrong rule there produces wrong edits. It is context the model carries all session, so keep it short.
- **Keeping delivered context short.** Aegis concatenates a repository's `./CLAUDE.md` from repo ⊕ each team ⊕ role ⊕ role-in-repo ⊕ person (`people/<name>/`), with `<!-- aegis:… -->` markers; global is a separate `~/.claude/CLAUDE.md`. `aegis_compose_context` mirrors that order client-side because no token-reachable route composes teams and person. If Aegis's composition changes, change `compose.go`. It also calls `GET /repos`: an unregistered slug gets only a notice from Aegis, so the result says `registered: false` with a note rather than implying the sections are delivered. The budgets (200 repository / 100 global lines) are constants there and are quoted in the instructions.
- **Prompts** (`prompts.go`) are the user-started workflows: `add-repo`, `add-guidance`, `health-check`. They refer to the ladder and the subagent review step in `Instructions` by name, so change both together. Write prompts are left out in read-only mode, like the write tools. Nothing is installed into the client's own config; the reviewer is a subagent the model starts with a brief from the instructions.

## The Aegis contract: admin API tokens

Aegis's admin API tokens are what this server uses. Keep the MCP paired with what a token can do there; when that changes, change the tools here.

- **Format and transport:** `aegt_<8 base32>_<secret>`, sent as `Authorization: Bearer`. A Bearer value without the `aegt_` prefix is treated as a session JWT, which is why a pasted 15-minute session token also works. `setup` warns about that, and refuses a developer API key (`aeg_…`).
- **Reach:** every route needing `editor` or less, plus `GET /admin/me`. Administrator routes answer **401** "API tokens cannot be used for this", so no tool may call one. Token management (`/auth/tokens`) is session-only.
- **Levels:** the owner's levels, read per request, lowered to the token's optional cap (`reader|collaborator|editor`). `GET /admin/me` reports the capped `level`/`highest_level`/`repo_levels`, and `api_token: {id, name, level, expires_at}` (`null` for a session; `expires_at` is RFC 3339 UTC). The account's name is `username`, not `name`. `tools.WritesAllowed` reads `highest_level` and `tools.TokenExpiry` reads `expires_at`. `main.canWrite` asks once at start: it drops the write tools below editor and warns on stderr when the token expires within 7 days. `setup` prints the expiry and warns within 14.
- **Refusals:** an unknown, revoked or expired token is 401 "this API token is not valid". A capped token gets the usual 403 `{required, held}` with `held` already capped.
- **What a token may do, and the tool for each:** files (the file tools), variables (`*_variable`), repo registry and import (`*_repo*`, but not read access setup, `PUT /repos/<id>/source`, which handles git credentials and stays in the UI), activity (`aegis_list_activity`, `aegis_show_change`), role preview. Per-user preview needs a user id that only administrators can list, so it has no tool.
- **Variable update quirk:** `PUT /variables/<name>` keeps the stored value when `value` is absent but **replaces the description with whatever is sent, empty included**. `updateVariable` therefore reads the current description and sends it back unless the caller gives one.
- **Timeouts:** the scan and import routes read a git repository, which Aegis allows up to 120 s, so they use `aegis.LongTimeout` (150 s) instead of the default 30 s.

## Testing

`make check` enforces **100% statement coverage of every function** (`scripts/coverage.sh`). A new branch needs a test. If a branch genuinely can't be reached, remove it rather than excluding it.

- `internal/aegis`: `httptest` servers per case (`serve`, `reply`, `closedServer`).
- `internal/tools`: end-to-end through the SDK's in-memory transports. `connect` uses `fakeAegis` (stateful files + ETags + If-Match). `connectTo` takes any handler, e.g. `routes{"GET /api/repos": {200, "…"}}` or `failing(403, …)`.
- `internal/config`: every test points `AEGIS_MCP_CONFIG` at a temp path, so the developer's real config is never read.
- `internal/setup`: a fake `claude` shell script (`fakeClaude`) records its arguments. `pty_linux_test.go` opens a real pseudo-terminal to cover the hidden token prompt; it skips where `/dev/ptmx` is missing, and the coverage gate then fails.
- `main_test.go`: the real program over `io.Pipe` (`TestSession`), setup followed by serving from the saved file, and the child-process `TestMainProcess`.
- Installers, outside the Go gate: `scripts/test-install.sh <dist>` (CI on Ubuntu and macOS) and `scripts/test-install.ps1 -Binary <exe>` (CI on Windows). Both check install, pinned version, checksum refusal and scripted setup against a fake release and a fake Aegis served by `scripts/test-server.py`. That helper binds a port the OS picks and writes it to a file, skips `http.server`'s reverse DNS lookup (it can stall on macOS), and is only used once it has answered a request. Don't go back to fixed ports or bare `python3 -m http.server`: that combination timed out on the macOS runner. To run the PowerShell suite here: `docker run --rm -v "$PWD":/r:ro -w /r mcr.microsoft.com/powershell:lts-alpine-3.20 sh -c 'apk add -q python3 && pwsh -File scripts/test-install.ps1 -Binary dist/aegis-mcp-linux-amd64'`.
- CI's `docker` job builds the image and checks that a reply to `initialize` is the only thing on stdout.

## Releases

Versioned by git tag, like Aegis. Alpha tags look like `v0.1.0-alpha.1`. **Before 1.0 every release is published as a normal GitHub release, alphas included**, because `releases/latest/download/` (what the installers and README use) skips pre-releases. From 1.0, a tag with a hyphen becomes a pre-release.

1. Add a `## vX.Y.Z — <date>` section to `CHANGELOG.md`, written for customers. `scripts/release-notes.sh <tag>` extracts it, and the workflow stops if it's missing.
2. Tag and push. `.github/workflows/release.yml` runs the checks and builds `aegis-mcp-<os>-<arch>[.exe]` for linux/darwin amd64+arm64 and windows/amd64. It adds `install.sh` and `install.ps1`, writes `SHA256SUMS` over all of them, runs the leak check, and pushes `ghcr.io/krathos-systems/aegis-mcp:<tag>` and `:latest` for linux/amd64+arm64. Then it creates the release; re-running for an existing tag replaces its assets and notes.

The installers fetch from `releases/latest/download/`, which GitHub resolves to the newest **non-pre-release**; that's the reason for the 0.x rule above. Until 1.0 the image's `latest` follows every release. The GHCR package is private when first created: make it public once in the package settings.

Releases are not signed yet. Aegis signs its CLI releases with an Ed25519 key (`SHA256SUMS.sig`), and the same scheme should come here before 1.0.
