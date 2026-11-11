package tools

// Instructions is sent to the client on connect. It tells a model how Aegis
// lays out its files and the few rules that make a write fail or misbehave.
// Keep it accurate to the server: a wrong rule here produces wrong edits.
const Instructions = `This server manages an organisation's Aegis: the instructions, skills, agents and settings that Aegis delivers to every developer's AI coding tools.

Files live in two planes, each laid out by layer:
- content: Markdown. <layer>/claude/CLAUDE.md, <layer>/claude/skills/<name>/SKILL.md (plus any files beside it), <layer>/claude/agents/<name>.md
- config: <layer>/claude/settings.json and <layer>/claude/mcp.json

Layers: global, teams/<team>, roles/<role>, repos/<repo>, repos/<repo>/roles/<role>.
- A developer has at most one role. The role owns settings, MCP servers, skills and agents.
- Teams contribute only a CLAUDE.md section, and follow their members into every repository.
- Settings resolve top-down: global, then role, then repo, then role-in-repo. Each layer overrides only the keys it names.
- <repo> is a repository slug from aegis_list_repos.

Variables: write {{ aegis.NAME }} (upper snake case) to reference a variable. Files store the placeholder; Aegis substitutes it on delivery. A secret variable may only be the entire value of an env entry of an MCP server that has a command in mcp.json. Anywhere else the write is refused.

Working rules:
- Call aegis_whoami first. Permission levels are reader < collaborator < editor < administrator, globally or per repository. Writes need editor.
- Read a file before changing it, and pass its etag to aegis_update_file or aegis_delete_file. On a conflict, read it again and reapply the change.
- Updates replace the whole file. Keep what you did not mean to change.
- Every change is committed to Aegis's history under the token's account and reaches developers on their next sync, so confirm significant changes with the user first.
- Use aegis_preview_role to check what a role receives after a change.`
