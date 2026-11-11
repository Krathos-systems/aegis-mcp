# Changelog

Each release has a section headed `## <tag> — <date>`. The release workflow
publishes that section as the release notes and refuses a tag without one.

## v0.1.0-alpha.1 — unreleased

First alpha. Needs an Aegis server that accepts admin tokens.

- Run as a local MCP server over stdio for Claude Code, Cursor and other MCP clients.
- Read tools: `aegis_whoami`, `aegis_list_files`, `aegis_read_file`, `aegis_list_repos`, `aegis_list_variables`, `aegis_preview_role`.
- Write tools: `aegis_create_file`, `aegis_update_file`, `aegis_delete_file`. Updates and deletes need the file's etag, so a change made by someone else in the meantime is never overwritten.
- `--read-only` (or `AEGIS_READ_ONLY=true`) offers only the read tools.
- One-line install for macOS, Linux and Windows. The installer checks the download against SHA256SUMS and needs no administrator rights.
- `aegis-mcp setup` asks for your Aegis address and token, checks them, saves them privately, and adds the server to Claude Code. It prints the config for other MCP clients.
- Docker image and a docker-compose.yml for running the server in a container.
- Binaries for Linux, macOS (Intel and Apple silicon) and Windows, with SHA256SUMS.
