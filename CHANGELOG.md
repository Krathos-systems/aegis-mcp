# Changelog

Each release has a section headed `## <tag> — <date>`. The release workflow
publishes that section as the release notes and refuses a tag without one.

## v0.1.0-alpha.3 — 2026-11-12

- New tools `aegis_list_teams_and_roles` and `aegis_create_team_or_role`, and prompts `add-team` and `add-role`. The model checks for an existing or similar name, asks what the team or role is for, and moves guidance that belongs there instead of copying it. Assigning people stays in the Aegis admin UI, and the model says so.

## v0.1.0-alpha.2 — 2026-11-12

- Works with Aegis API tokens. Make one on your account page under **API tokens**.
- New tools for everything a token can do: manage variables, register repositories and import their existing files, and read the history of changes.
- A token limited to collaborator gets only the tools that read.
- `aegis-mcp setup` names the token it signed in with and when it expires. It explains when it's given a developer API key or a sign-in session instead.
- The server warns when its token expires within a week.
- The model now knows where each kind of guidance belongs: the repository, team, role, role-in-repository, personal or global layer, or a skill or setting instead of instructions. It keeps what developers receive short and free of repeats.
- New tool `aegis_compose_context` shows a repository's `CLAUDE.md` as a developer receives it, with line counts and repeated lines, and checks drafts before they are written.
- New prompts: `add-repo`, `add-guidance` and `health-check`.
- The model explains what it is doing in plain words, asks who guidance is for when that is unclear, and shows its plan before changing anything.
- Creating or updating a Markdown file reports its line count.

## v0.1.0-alpha.1 — 2026-11-11

First alpha. Needs an Aegis server that accepts admin tokens.

- Run as a local MCP server over stdio for Claude Code, Cursor and other MCP clients.
- Read tools: `aegis_whoami`, `aegis_list_files`, `aegis_read_file`, `aegis_list_repos`, `aegis_list_variables`, `aegis_preview_role`.
- Write tools: `aegis_create_file`, `aegis_update_file`, `aegis_delete_file`. Updates and deletes need the file's etag, so a change made by someone else in the meantime is never overwritten.
- `--read-only` (or `AEGIS_READ_ONLY=true`) offers only the read tools.
- One-line install for macOS, Linux and Windows. The installer checks the download against SHA256SUMS and needs no administrator rights.
- `aegis-mcp setup` asks for your Aegis address and token, checks them, saves them privately, and adds the server to Claude Code. It prints the config for other MCP clients.
- Docker image and a docker-compose.yml for running the server in a container.
- Binaries for Linux, macOS (Intel and Apple silicon) and Windows, with SHA256SUMS.
