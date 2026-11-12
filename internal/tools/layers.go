package tools

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Krathos-systems/aegis-mcp/internal/aegis"
)

// A team or role exists in Aegis once a file sits under teams/<name>/ or
// roles/<name>/; there is no other registry a token can write. Who holds one
// is set by an administrator, which no token can do, so every result says so.

// layerName is the admin UI's rule for a new team or role name, capped at
// the server's 64 characters.
var layerName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

const assignNote = "Teams and roles reach nobody until an administrator assigns people to them in the Aegis admin UI (Users). A person has one role and one or more teams."

type layerInfo struct {
	Name  string   `json:"name"`
	Files []string `json:"files" jsonschema:"files in the layer itself, from both planes, relative to it"`
	Repos []string `json:"repos,omitempty" jsonschema:"repositories with a repos/<repo>/roles/<role>/ layer for this role"`
}

type teamsRolesOut struct {
	Teams []layerInfo `json:"teams"`
	Roles []layerInfo `json:"roles"`
	Note  string      `json:"note"`
}

type createLayerIn struct {
	Kind     string `json:"kind" jsonschema:"team or role"`
	Name     string `json:"name" jsonschema:"lower case letters, digits and hyphens, e.g. payments"`
	ClaudeMD string `json:"claude_md,omitempty" jsonschema:"the layer's first CLAUDE.md, written by the placement ladder; leave out for a one-line heading"`
}

type createLayerOut struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Path  string `json:"path"`
	ETag  string `json:"etag"`
	Lines int    `json:"lines"`
	Note  string `json:"note"`
}

func (h *handlers) listTeamsAndRoles(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, teamsRolesOut, error) {
	teams, roles, err := h.teamsAndRoles(ctx)
	if err != nil {
		return nil, teamsRolesOut{}, err
	}
	return nil, teamsRolesOut{Teams: teams, Roles: roles, Note: assignNote}, nil
}

func (h *handlers) createTeamOrRole(ctx context.Context, _ *mcp.CallToolRequest, in createLayerIn) (*mcp.CallToolResult, createLayerOut, error) {
	if in.Kind != "team" && in.Kind != "role" {
		return nil, createLayerOut{}, fmt.Errorf("kind must be team or role")
	}
	if !layerName.MatchString(in.Name) {
		return nil, createLayerOut{}, fmt.Errorf("%q is not a valid name: use lower case letters, digits and hyphens, starting with a letter or digit", in.Name)
	}
	teams, roles, err := h.teamsAndRoles(ctx)
	if err != nil {
		return nil, createLayerOut{}, err
	}
	existing := teams
	if in.Kind == "role" {
		existing = roles
	}
	for _, l := range existing {
		if l.Name == in.Name && len(l.Files) > 0 {
			return nil, createLayerOut{}, fmt.Errorf("the %s %s already exists; read its files with aegis_read_file and change them instead", in.Kind, in.Name)
		}
	}
	body := in.ClaudeMD
	if strings.TrimSpace(body) == "" {
		body = "# " + in.Name + "\n"
	}
	path := in.Kind + "s/" + in.Name + "/claude/CLAUDE.md"
	r, err := h.c.CreateFile(ctx, aegis.Content, path, body)
	if err != nil {
		return nil, createLayerOut{}, explain(err)
	}
	return nil, createLayerOut{Kind: in.Kind, Name: in.Name, Path: path, ETag: r.ETag, Lines: lineCount(body), Note: assignNote}, nil
}

// teamsAndRoles lists every team and role with a file in either plane, and
// for each role the repositories that have a role-in-repository layer for it.
func (h *handlers) teamsAndRoles(ctx context.Context) (teams, roles []layerInfo, err error) {
	var files []string
	for _, p := range []aegis.Plane{aegis.Content, aegis.Config} {
		got, err := h.c.ListFiles(ctx, p)
		if err != nil {
			return nil, nil, explain(err)
		}
		files = append(files, got...)
	}
	teams, roles = groupLayers(files)
	return teams, roles, nil
}

func groupLayers(files []string) (teams, roles []layerInfo) {
	t, r := map[string]*layerInfo{}, map[string]*layerInfo{}
	get := func(m map[string]*layerInfo, name string) *layerInfo {
		if m[name] == nil {
			m[name] = &layerInfo{Name: name, Files: []string{}}
		}
		return m[name]
	}
	for _, f := range files {
		parts := strings.SplitN(f, "/", 3)
		if len(parts) < 3 || parts[1] == "" {
			continue // e.g. the legacy teams/<name>.md, which is not a layer
		}
		switch parts[0] {
		case "teams":
			l := get(t, parts[1])
			l.Files = append(l.Files, parts[2])
		case "roles":
			l := get(r, parts[1])
			l.Files = append(l.Files, parts[2])
		case "repos":
			rest := strings.SplitN(parts[2], "/", 3)
			if len(rest) == 3 && rest[0] == "roles" && rest[1] != "" {
				l := get(r, rest[1])
				if !slices.Contains(l.Repos, parts[1]) {
					l.Repos = append(l.Repos, parts[1])
				}
			}
		}
	}
	return sorted(t), sorted(r)
}

func sorted(m map[string]*layerInfo) []layerInfo {
	out := []layerInfo{}
	for _, l := range m {
		sort.Strings(l.Files)
		sort.Strings(l.Repos)
		out = append(out, *l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
