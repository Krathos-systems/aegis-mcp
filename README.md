# Aegis MCP server

An [MCP](https://modelcontextprotocol.io) server for [Aegis](https://aegis.krathos.systems). It lets an AI client such as Claude Code or Cursor read and change your organisation's Aegis context and configuration (CLAUDE.md instructions, skills, agents, `settings.json` and `mcp.json`) so you don't have to edit them by hand in the Aegis UI.

> **Alpha.** Tools and their arguments may change between releases.

## How it works

`aegis-mcp` runs on your machine. Your AI client starts it and talks to it over stdio, and it calls the Aegis API with an admin token. Aegis checks every call against the token's account, so the MCP server can do exactly what that account can do in the Aegis UI and nothing more. Every change is recorded in Aegis's history under that account.

## Install

Download the binary for your platform from the [latest release](https://github.com/Krathos-systems/aegis-mcp/releases), check it against `SHA256SUMS`, and put it on your `PATH`:

```bash
sha256sum --check --ignore-missing SHA256SUMS
install -m 755 aegis-mcp-linux-amd64 ~/.local/bin/aegis-mcp
aegis-mcp --version
```

## Configure

| Variable | |
|---|---|
| `AEGIS_SERVER` | Your Aegis address, e.g. `https://acme.example.com`. Required. |
| `AEGIS_TOKEN_FILE` | A file holding your Aegis admin token. Recommended. |
| `AEGIS_TOKEN` | The token itself, instead of a file. |
| `AEGIS_READ_ONLY` | `true` to offer only the tools that read. Same as `--read-only`. |

The token is never accepted as a command-line flag, because other users on the machine can see a process's arguments. Keep the token file private (`chmod 600`).

### Claude Code

```bash
claude mcp add aegis \
  -e AEGIS_SERVER=https://acme.example.com \
  -e AEGIS_TOKEN_FILE="$HOME/.config/aegis/mcp-token" \
  -- aegis-mcp
```

### Other clients

Any MCP client that can start a stdio server works. The usual JSON form:

```json
{
  "mcpServers": {
    "aegis": {
      "command": "aegis-mcp",
      "env": {
        "AEGIS_SERVER": "https://acme.example.com",
        "AEGIS_TOKEN_FILE": "/path/to/mcp-token"
      }
    }
  }
}
```

## Tools

| Tool | Does |
|---|---|
| `aegis_whoami` | The token's account, its permission levels, and the server version |
| `aegis_list_files` | Files in the `content` or `config` plane |
| `aegis_read_file` | One file as stored, plus the etag needed to change it |
| `aegis_create_file` | Create a file that does not exist yet |
| `aegis_update_file` | Replace a file's whole body (needs its etag) |
| `aegis_delete_file` | Delete a file (needs its etag) |
| `aegis_list_repos` | Registered repositories and their slugs |
| `aegis_list_variables` | Variables and where they are used (never secret values) |
| `aegis_preview_role` | What one role receives in one repository after all layers merge |

Updates and deletes need the etag from the last read. If someone changed the file since, Aegis refuses the change instead of overwriting their work, and the model is told to read the file again.

## Development

Needs Docker; the Makefile runs Go 1.26 in a container.

```bash
make check                    # gofmt, go vet, tests with the 100% coverage gate
make test TEST=TestCleanPath  # run some tests
make build                    # ./aegis-mcp for linux/amd64
make dist VERSION=v0.1.0      # every platform into dist/, with SHA256SUMS
```

## Releases

Versions are git tags (`vMAJOR.MINOR.PATCH`, with `-alpha.N` or `-beta.N` for pre-releases). Pushing a tag builds every platform and publishes a GitHub release. The tag's section of [CHANGELOG.md](CHANGELOG.md) becomes the release notes, so add it before tagging.

## License

[Apache License 2.0](LICENSE).
