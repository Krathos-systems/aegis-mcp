package tools

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func layersFake() *fakeAegis {
	return &fakeAegis{files: map[string]string{
		"content/teams/pay/claude/CLAUDE.md":          "- Review in pairs.\n",
		"content/teams/legacy.md":                     "v1 seed",
		"content/roles/fe/claude/CLAUDE.md":           "- Use the design system.\n",
		"content/roles/fe/claude/skills/x/SKILL.md":   "skill",
		"config/roles/fe/claude/settings.json":        "{}",
		"content/repos/web/roles/fe/claude/CLAUDE.md": "- web\n",
		"config/repos/web/roles/fe/claude/mcp.json":   "{}",
		"content/repos/app/roles/qa/claude/CLAUDE.md": "- app\n",
		"content/repos/web/claude/CLAUDE.md":          "- repo\n",
	}}
}

func TestListTeamsAndRoles(t *testing.T) {
	cs := connectTo(t, layersFake(), "good", true)
	out, text, isErr := call(t, cs, "aegis_list_teams_and_roles", nil)
	if isErr {
		t.Fatal(text)
	}
	if got := fmt.Sprint(out["teams"]); got != "[map[files:[claude/CLAUDE.md] name:pay]]" {
		t.Errorf("teams %s", got)
	}
	want := "[map[files:[claude/CLAUDE.md claude/settings.json claude/skills/x/SKILL.md] name:fe repos:[web]] map[files:[] name:qa repos:[app]]]"
	if got := fmt.Sprint(out["roles"]); got != want {
		t.Errorf("roles %s", got)
	}
	if !strings.Contains(out["note"].(string), "administrator") {
		t.Errorf("note %v", out["note"])
	}
}

func TestListTeamsAndRolesErrors(t *testing.T) {
	for name, h := range map[string]routes{
		"content": {},
		"config":  {"GET /api/content/files": {200, `[]`}},
	} {
		cs := connectTo(t, h, "good", true)
		if _, text, isErr := call(t, cs, "aegis_list_teams_and_roles", nil); !isErr {
			t.Errorf("%s: no error: %s", name, text)
		}
	}
}

func TestGroupLayers(t *testing.T) {
	teams, roles := groupLayers([]string{"teams//x", "repos/web/roles//x", "repos/web/claude/CLAUDE.md", "global/claude/CLAUDE.md", "roles", "repos/a/roles/r/x", "repos/a/roles/r/y"})
	if len(teams) != 0 || !reflect.DeepEqual(roles, []layerInfo{{Name: "r", Files: []string{}, Repos: []string{"a"}}}) {
		t.Errorf("teams %v roles %v", teams, roles)
	}
}

func TestCreateTeamOrRole(t *testing.T) {
	fake := layersFake()
	cs := connectTo(t, fake, "good", false)

	out, text, isErr := call(t, cs, "aegis_create_team_or_role", map[string]any{"kind": "team", "name": "ops"})
	if isErr || out["path"] != "teams/ops/claude/CLAUDE.md" || out["lines"] != float64(1) || out["etag"] == "" {
		t.Fatalf("team: %v %s", out, text)
	}
	if fake.files["content/teams/ops/claude/CLAUDE.md"] != "# ops\n" {
		t.Errorf("team body %q", fake.files["content/teams/ops/claude/CLAUDE.md"])
	}

	// A role known only from a role-in-repository layer has no layer of its own yet.
	out, text, isErr = call(t, cs, "aegis_create_team_or_role", map[string]any{"kind": "role", "name": "qa", "claude_md": "- Test first.\n- Then ship.\n"})
	if isErr || out["path"] != "roles/qa/claude/CLAUDE.md" || out["lines"] != float64(2) || !strings.Contains(out["note"].(string), "admin UI") {
		t.Fatalf("role: %v %s", out, text)
	}

	for _, args := range []map[string]any{
		{"kind": "team", "name": "Pay Team"},
		{"kind": "team", "name": "-x"},
		{"kind": "team", "name": strings.Repeat("a", 65)},
		{"kind": "team", "name": "pay"},
		{"kind": "role", "name": "fe"},
	} {
		if _, text, isErr := call(t, cs, "aegis_create_team_or_role", args); !isErr {
			t.Errorf("%v accepted: %s", args, text)
		}
	}
	if _, text, _ := call(t, cs, "aegis_create_team_or_role", map[string]any{"kind": "team", "name": "pay"}); !strings.Contains(text, "already exists") {
		t.Errorf("existing: %s", text)
	}
}

func TestCreateTeamOrRoleErrors(t *testing.T) {
	for name, h := range map[string]routes{
		"list":   {},
		"create": {"GET /api/content/files": {200, `[]`}, "GET /api/config/files": {200, `[]`}},
	} {
		cs := connectTo(t, h, "good", false)
		if _, text, isErr := call(t, cs, "aegis_create_team_or_role", map[string]any{"kind": "team", "name": "ops"}); !isErr {
			t.Errorf("%s: no error: %s", name, text)
		}
	}
}

// The schema limits kind to team or role; the handler guards it again.
func TestCreateTeamOrRoleRefusesUnknownKindDirectly(t *testing.T) {
	h := &handlers{}
	if _, _, err := h.createTeamOrRole(t.Context(), nil, createLayerIn{Kind: "group", Name: "x"}); err == nil {
		t.Error("kind group accepted")
	}
}
