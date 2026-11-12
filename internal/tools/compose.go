package tools

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Krathos-systems/aegis-mcp/internal/aegis"
)

// Line budgets the instructions give the model. A repository's ./CLAUDE.md is
// everything one developer receives there; global loads in every repository.
const (
	repoBudget   = 200
	globalBudget = 100
)

type composeIn struct {
	Repo   string            `json:"repo" jsonschema:"repository slug, from aegis_list_repos"`
	Role   string            `json:"role,omitempty" jsonschema:"role name, as in roles/<role>/; leave out to compose without a role"`
	Teams  []string          `json:"teams,omitempty" jsonschema:"team names; leave out to include every team that has a CLAUDE.md, which gives an upper bound"`
	Person string            `json:"person,omitempty" jsonschema:"a username, to include their people/<name>/ section"`
	Drafts map[string]string `json:"drafts,omitempty" jsonschema:"content-plane path to a proposed whole body, used instead of the stored file; an empty body stands for deleting the file"`
}

type section struct {
	Marker string `json:"marker"`
	Path   string `json:"path"`
	Lines  int    `json:"lines"`
	Draft  bool   `json:"draft,omitempty"`
}

type repeat struct {
	Line     string   `json:"line"`
	Count    int      `json:"count"`
	Sections []string `json:"sections"`
}

type composeOut struct {
	Registered       bool                `json:"registered" jsonschema:"false: developers there receive only a notice asking for the repository to be registered, not these sections"`
	ClaudeMD         string              `json:"claude_md" jsonschema:"the repository's ./CLAUDE.md as this developer receives it once registered"`
	Sections         []section           `json:"sections"`
	TotalLines       int                 `json:"total_lines"`
	Budget           int                 `json:"budget"`
	OverBudget       bool                `json:"over_budget"`
	Global           string              `json:"global" jsonschema:"~/.claude/CLAUDE.md, loaded alongside it"`
	GlobalLines      int                 `json:"global_lines"`
	GlobalBudget     int                 `json:"global_budget"`
	GlobalOverBudget bool                `json:"global_over_budget"`
	Repeated         []repeat            `json:"repeated" jsonschema:"lines that appear more than once across both files, ignoring case and list markers"`
	Skills           map[string][]string `json:"skills" jsonschema:"skill names by layer; a later layer's same name replaces an earlier one"`
	Agents           map[string][]string `json:"agents" jsonschema:"agent names by layer; a later layer's same name replaces an earlier one"`
	Notes            []string            `json:"notes,omitempty"`
}

// part is one composed section: the Aegis origin marker and its body.
type part struct {
	marker, path, body string
	draft              bool
}

func (h *handlers) composeContext(ctx context.Context, _ *mcp.CallToolRequest, in composeIn) (*mcp.CallToolResult, composeOut, error) {
	if err := checkNames(in); err != nil {
		return nil, composeOut{}, err
	}
	listed, err := h.c.ListFiles(ctx, aegis.Content)
	if err != nil {
		return nil, composeOut{}, explain(err)
	}
	exists := map[string]bool{}
	for _, f := range listed {
		exists[f] = true
	}
	for p, b := range in.Drafts {
		exists[p] = strings.TrimSpace(b) != ""
	}

	repos, err := h.c.Repos(ctx)
	if err != nil {
		return nil, composeOut{}, explain(err)
	}

	out := composeOut{Budget: repoBudget, GlobalBudget: globalBudget, Registered: registered(repos, in.Repo)}
	if !out.Registered {
		out.Notes = append(out.Notes, fmt.Sprintf("%q is not a registered repository: developers there receive only a notice asking for it to be registered, not the sections below; register it with aegis_register_repo", in.Repo))
	}
	teams := in.Teams
	if teams == nil {
		teams = layerNames(exists, "teams/")
		if len(teams) > 0 {
			out.Notes = append(out.Notes, "teams defaulted to every team with a CLAUDE.md; a developer is usually on fewer, so the total is an upper bound")
		}
	}

	type layer struct{ marker, dir string }
	layers := []layer{{"aegis:repo=" + in.Repo, "repos/" + in.Repo}}
	for _, t := range teams {
		layers = append(layers, layer{"aegis:team=" + t, "teams/" + t})
	}
	if in.Role != "" {
		layers = append(layers,
			layer{"aegis:role=" + in.Role, "roles/" + in.Role},
			layer{"aegis:role-at-repo=" + in.Role + "@" + in.Repo, "repos/" + in.Repo + "/roles/" + in.Role})
	}
	if in.Person != "" {
		layers = append(layers, layer{"aegis:person=" + in.Person, "people/" + in.Person})
	}

	read := func(marker, dir string) (*part, error) {
		p := dir + "/claude/CLAUDE.md"
		if !exists[p] {
			return nil, nil
		}
		if b, ok := in.Drafts[p]; ok {
			return &part{marker, p, b, true}, nil
		}
		f, err := h.c.ReadFile(ctx, aegis.Content, p)
		if err != nil {
			return nil, explain(err)
		}
		return &part{marker, p, f.Body, false}, nil
	}

	var parts []part
	for _, l := range layers {
		pt, err := read(l.marker, l.dir)
		if err != nil {
			return nil, composeOut{}, err
		}
		if pt != nil && strings.TrimSpace(pt.body) != "" {
			parts = append(parts, *pt)
		}
	}
	global, err := read("aegis:global", "global")
	if err != nil {
		return nil, composeOut{}, err
	}

	out.ClaudeMD = compose(parts)
	out.Sections = []section{}
	for _, pt := range parts {
		out.Sections = append(out.Sections, section{pt.marker, pt.path, lineCount(strings.TrimSpace(pt.body)), pt.draft})
	}
	out.TotalLines = lineCount(out.ClaudeMD)
	out.OverBudget = out.TotalLines > repoBudget
	all := parts
	if global != nil {
		out.Global = global.body
		out.GlobalLines = lineCount(strings.TrimSpace(global.body))
		out.GlobalOverBudget = out.GlobalLines > globalBudget
		all = append(all[:len(all):len(all)], *global)
	}
	out.Repeated = repeats(all)

	entryLayers := []string{"global"}
	if in.Role != "" {
		entryLayers = append(entryLayers, "roles/"+in.Role)
	}
	entryLayers = append(entryLayers, "repos/"+in.Repo)
	if in.Role != "" {
		entryLayers = append(entryLayers, "repos/"+in.Repo+"/roles/"+in.Role)
	}
	out.Skills, out.Agents = map[string][]string{}, map[string][]string{}
	for _, l := range entryLayers {
		if s := entries(exists, l+"/claude/skills/", "/SKILL.md"); len(s) > 0 {
			out.Skills[l] = s
		}
		if a := entries(exists, l+"/claude/agents/", ".md"); len(a) > 0 {
			out.Agents[l] = a
		}
	}
	return nil, out, nil
}

// registered reports whether slug is in a GET /repos reply: a list of remotes,
// each naming the slug it maps to.
func registered(repos any, slug string) bool {
	list, _ := repos.([]any)
	for _, r := range list {
		if m, _ := r.(map[string]any); m["repo"] == slug {
			return true
		}
	}
	return false
}

// checkNames refuses a name that would not be a single path segment, so a
// layer cannot be composed from somewhere else in the tree.
func checkNames(in composeIn) error {
	if in.Repo == "" {
		return fmt.Errorf("repo is required")
	}
	for _, t := range in.Teams {
		if t == "" {
			return fmt.Errorf("a team name is empty")
		}
	}
	for _, n := range append([]string{in.Repo, in.Role, in.Person}, in.Teams...) {
		if n == "." || n == ".." || strings.ContainsAny(n, `/\`) {
			return fmt.Errorf("%q is not a single name", n)
		}
	}
	return nil
}

// layerNames lists the names under prefix (e.g. "teams/") that have a CLAUDE.md.
func layerNames(exists map[string]bool, prefix string) []string {
	names := []string{}
	for p, ok := range exists {
		name, rest, found := strings.Cut(strings.TrimPrefix(p, prefix), "/")
		if ok && found && strings.HasPrefix(p, prefix) && rest == "claude/CLAUDE.md" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// entries lists skill or agent names: the segment between prefix and suffix.
func entries(exists map[string]bool, prefix, suffix string) []string {
	var names []string
	for p, ok := range exists {
		name := strings.TrimSuffix(strings.TrimPrefix(p, prefix), suffix)
		if ok && strings.HasPrefix(p, prefix) && strings.HasSuffix(p, suffix) && name != "" && !strings.Contains(name, "/") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// compose joins sections the way Aegis does, each inside its origin markers.
func compose(parts []part) string {
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = fmt.Sprintf("<!-- %s -->\n%s\n<!-- /%s -->", p.marker, strings.TrimSpace(p.body), p.marker)
	}
	return strings.Join(out, "\n\n")
}

func lineCount(s string) int {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// normalize reduces a line to what makes it the same rule as another: case,
// spacing and list markers removed. It returns "" for lines that carry no rule
// of their own — blanks, headings, fences, markers and rules without letters.
func normalize(line string) string {
	s := strings.TrimSpace(line)
	if s == "" || strings.HasPrefix(s, "#") || strings.HasPrefix(s, "```") || strings.HasPrefix(s, "<!--") {
		return ""
	}
	s = strings.TrimLeft(s, "-*+> ")
	if i := strings.IndexAny(s, ".)"); i > 0 && i < 4 && strings.Trim(s[:i], "0123456789") == "" {
		s = s[i+1:]
	}
	s = strings.ToLower(strings.Join(strings.Fields(strings.TrimRight(s, ".;:! ")), " "))
	if !strings.ContainsFunc(s, func(r rune) bool { return r >= 'a' && r <= 'z' }) {
		return ""
	}
	return s
}

// repeats finds lines that occur more than once across parts, in the order
// they first appear, with the sections each one is in.
func repeats(parts []part) []repeat {
	type seen struct {
		r     repeat
		order int
	}
	byLine := map[string]*seen{}
	for _, p := range parts {
		for _, line := range strings.Split(p.body, "\n") {
			n := normalize(line)
			if n == "" {
				continue
			}
			s, ok := byLine[n]
			if !ok {
				s = &seen{repeat{Line: strings.TrimSpace(line)}, len(byLine)}
				byLine[n] = s
			}
			s.r.Count++
			if !slices.Contains(s.r.Sections, p.marker) {
				s.r.Sections = append(s.r.Sections, p.marker)
			}
		}
	}
	var found []*seen
	for _, s := range byLine {
		if s.r.Count > 1 {
			found = append(found, s)
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].order < found[j].order })
	out := []repeat{}
	for _, s := range found {
		out = append(out, s.r)
	}
	return out
}
