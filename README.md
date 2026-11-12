# Aegis MCP server

An [MCP](https://modelcontextprotocol.io) server for [Aegis](https://aegis.krathos.systems). It lets an AI client such as Claude Code or Cursor read and change your organisation's Aegis context and configuration (CLAUDE.md instructions, skills, agents, `settings.json` and `mcp.json`) so you don't have to edit them by hand in the Aegis UI.

> **Alpha.** Tools and their arguments may change between releases.

## How it works

`aegis-mcp` runs on your machine. Your AI client starts it and talks to it over stdio, and it calls the Aegis API with an admin token. Aegis checks every call against the token's account, so the MCP server can do exactly what that account can do in the Aegis UI and nothing more. Every change is recorded in Aegis's history under that account.

## Get an API token

The server acts with an Aegis **API token**, which starts with `aegt_`. To make one, open your account page in Aegis and go to **API tokens**. Give it a name, choose when it expires, and copy it; it's shown only once. A token acts as you, at most at editor level, so it can manage files, variables and repositories but never people, permissions or settings.

For a server that should only read, limit the token to collaborator. The server then offers only the tools that read.

An API key from the `aegis` command (starting with `aeg_`) is a different thing and won't work here.

## Install

**macOS and Linux**

```bash
curl -fsSL https://github.com/Krathos-systems/aegis-mcp/releases/latest/download/install.sh | sh
```

**Windows** (PowerShell)

```powershell
irm https://github.com/Krathos-systems/aegis-mcp/releases/latest/download/install.ps1 | iex
```

That's the whole install. The script downloads `aegis-mcp` for your system, checks it against the release's `SHA256SUMS`, and puts it in `~/.local/bin` (Windows: `%LOCALAPPDATA%\Programs\aegis-mcp`, added to your PATH). It never needs `sudo` or an administrator prompt. Then it runs `aegis-mcp setup`, which:

1. asks for your Aegis address and API token (the token is not shown as you type),
2. checks them with Aegis, so a typo is caught now rather than in your AI client,
3. saves them in a file only you can read,
4. adds the server to Claude Code if you have it, and prints the config for any other MCP client.

Run `aegis-mcp setup` again at any time, for example with a new token.

Installer options, as environment variables: `AEGIS_MCP_VERSION=v0.1.0` installs a specific release, `AEGIS_MCP_INSTALL_DIR` picks the directory, and `AEGIS_MCP_NO_SETUP=1` skips setup. With `AEGIS_SERVER` and `AEGIS_TOKEN` set, setup runs without questions, which suits scripts and provisioning.

### Docker

The image is `ghcr.io/krathos-systems/aegis-mcp`. With [docker-compose.yml](docker-compose.yml):

```bash
curl -fsSLO https://raw.githubusercontent.com/Krathos-systems/aegis-mcp/main/docker-compose.yml
curl -fsSL https://raw.githubusercontent.com/Krathos-systems/aegis-mcp/main/aegis-mcp.env.example -o aegis-mcp.env
chmod 600 aegis-mcp.env        # then put your address and token in it
claude mcp add aegis -- docker compose -f "$PWD/docker-compose.yml" run --rm -T aegis-mcp
```

Your AI client starts a fresh container for each session. For other clients, use `docker` as the command with the arguments `compose -f /full/path/to/docker-compose.yml run --rm -T aegis-mcp`. Without compose: `docker run -i --rm --env-file aegis-mcp.env ghcr.io/krathos-systems/aegis-mcp`. If Aegis runs on the same machine, use `http://host.docker.internal` as its address.

### By hand

Download your platform's binary and `SHA256SUMS` from the [latest release](https://github.com/Krathos-systems/aegis-mcp/releases), then:

```bash
sha256sum --check --ignore-missing SHA256SUMS
install -m 755 aegis-mcp-linux-amd64 ~/.local/bin/aegis-mcp
aegis-mcp setup
```

## Configuration

`aegis-mcp setup` saves your address and token to `aegis-mcp/config.json` in your user config directory (`~/.config` on Linux, `~/Library/Application Support` on macOS, `%AppData%` on Windows). These environment variables take precedence over it, which is how the Docker setup works:

| Variable | |
|---|---|
| `AEGIS_SERVER` | Your Aegis address, e.g. `https://acme.example.com` |
| `AEGIS_TOKEN` | Your Aegis API token |
| `AEGIS_TOKEN_FILE` | A file holding the token, instead of `AEGIS_TOKEN` |
| `AEGIS_READ_ONLY` | `true` to offer only the tools that read (same as `--read-only`) |
| `AEGIS_MCP_CONFIG` | Use a different config file |

The token is never accepted as a command-line flag, because other users on the machine can see a process's arguments.

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
| `aegis_compose_context` | A repository's `CLAUDE.md` as one developer receives it (repository, teams, role and personal sections), with line counts against a budget and repeated lines. Accepts drafts, so a change can be checked before it is written. |
| `aegis_list_activity`, `aegis_show_change` | Who changed what, and the diff of one change |
| `aegis_create_variable`, `aegis_update_variable`, `aegis_delete_variable` | Manage variables. A secret's value can be set but never read back. |
| `aegis_register_repo`, `aegis_move_repo`, `aegis_unregister_repo` | Map git remotes to repository slugs |
| `aegis_scan_repo_import`, `aegis_import_repo` | Bring a repository's existing AI files into Aegis |

### Prompts

Workflows you start by name (in Claude Code, `/mcp__aegis__<name>`):

| Prompt | Does |
|---|---|
| `add-repo` | Bring a repository's existing AI instructions into Aegis, split across the right layers rather than copied as one long file |
| `add-guidance` | Decide where one rule belongs (or that it needs no line at all) and add it without duplicating what is there |
| `health-check` | Explain what developers receive that is too long, repeated or in the wrong layer, and suggest fixes. Applies only the ones you pick. |

Before a change to a `CLAUDE.md`, the model checks what developers will receive, with a subagent where the client has them, so whole files stay out of the main conversation.

Updates and deletes need the etag from the last read. If someone changed the file since, Aegis refuses the change instead of overwriting their work, and the model is told to read the file again.

## Development

Needs Docker; the Makefile runs Go 1.26 in a container.

```bash
make check                    # gofmt, go vet, tests with the 100% coverage gate
make test TEST=TestCleanPath  # run some tests
make build                    # ./aegis-mcp for linux/amd64
make dist VERSION=v0.1.0      # every platform into dist/, with SHA256SUMS
make test-install             # the installer tests, against a fake local release
make image                    # the Docker image, as aegis-mcp:dev
```

## Releases

Versions are git tags (`vMAJOR.MINOR.PATCH`, with `-alpha.N` or `-beta.N` for pre-releases; before 1.0 these are published as ordinary releases so the installer finds them). Pushing a tag builds every platform, publishes a GitHub release with the binaries and installers, and pushes the Docker image. The tag's section of [CHANGELOG.md](CHANGELOG.md) becomes the release notes, so add it before tagging.

## License

[Apache License 2.0](LICENSE).
