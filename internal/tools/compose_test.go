package tools

import (
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// withRepos answers GET /api/repos with body and sends the rest to h.
func withRepos(h http.Handler, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/repos" {
			io.WriteString(w, body)
			return
		}
		h.ServeHTTP(w, r)
	})
}

const webRegistered = `[{"id":1,"remote_url":"github.com/acme/web","repo":"web"}]`

func composeFake() http.Handler {
	return withRepos(&fakeAegis{files: map[string]string{
		"content/global/claude/CLAUDE.md":                 "# Global\n- Never commit secrets.\n",
		"content/global/claude/agents/reviewer.md":        "agent",
		"content/repos/web/claude/CLAUDE.md":              "# Web\n- Run `make test`.\n- never  commit secrets\n",
		"content/repos/web/claude/skills/deploy/SKILL.md": "skill",
		"content/teams/pay/claude/CLAUDE.md":              "- Review in pairs.\n",
		"content/teams/pay/claude/skills/x/SKILL.md":      "team skills are not delivered",
		"content/teams/ops/claude/CLAUDE.md":              "- Keep the on-call rota.\n",
		"content/teams/blank/claude/CLAUDE.md":            "  \n",
		"content/roles/fe/claude/CLAUDE.md":               "1. Use the design system.\n",
		"content/roles/fe/claude/skills/deploy/SKILL.md":  "skill",
		"content/repos/web/roles/fe/claude/CLAUDE.md":     "* run `MAKE test`.\n",
		"content/repos/web/roles/fe/claude/agents/a.md":   "agent",
		"content/people/ada/claude/CLAUDE.md":             "- Terse replies.\n",
	}}, webRegistered)
}

func TestComposeContext(t *testing.T) {
	cs := connectTo(t, composeFake(), "good", true)
	out, text, isErr := call(t, cs, "aegis_compose_context", map[string]any{"repo": "web", "role": "fe", "person": "ada"})
	if isErr {
		t.Fatal(text)
	}

	var markers []string
	for _, s := range out["sections"].([]any) {
		markers = append(markers, s.(map[string]any)["marker"].(string))
	}
	want := []string{"aegis:repo=web", "aegis:team=ops", "aegis:team=pay", "aegis:role=fe", "aegis:role-at-repo=fe@web", "aegis:person=ada"}
	if !reflect.DeepEqual(markers, want) {
		t.Errorf("sections %v, want %v", markers, want)
	}
	md := out["claude_md"].(string)
	if !strings.HasPrefix(md, "<!-- aegis:repo=web -->\n# Web\n") || !strings.HasSuffix(md, "- Terse replies.\n<!-- /aegis:person=ada -->") {
		t.Errorf("claude_md:\n%s", md)
	}
	if out["total_lines"].(float64) != float64(lineCount(md)) || out["over_budget"] != false || out["budget"].(float64) != repoBudget {
		t.Errorf("totals %v", out)
	}
	if out["global_lines"].(float64) != 2 || out["global_over_budget"] != false {
		t.Errorf("global %v", out)
	}
	if notes := fmt.Sprint(out["notes"]); !strings.Contains(notes, "upper bound") {
		t.Errorf("notes %s", notes)
	}

	rep := out["repeated"].([]any)
	if len(rep) != 2 {
		t.Fatalf("repeated %v", rep)
	}
	first, second := rep[0].(map[string]any), rep[1].(map[string]any)
	if first["line"] != "- Run `make test`." || fmt.Sprint(first["sections"]) != "[aegis:repo=web aegis:role-at-repo=fe@web]" {
		t.Errorf("first repeat %v", first)
	}
	if fmt.Sprint(second["sections"]) != "[aegis:repo=web aegis:global]" || second["count"].(float64) != 2 {
		t.Errorf("second repeat %v", second)
	}

	if got := fmt.Sprint(out["skills"]); got != "map[repos/web:[deploy] roles/fe:[deploy]]" {
		t.Errorf("skills %s", got)
	}
	if got := fmt.Sprint(out["agents"]); got != "map[global:[reviewer] repos/web/roles/fe:[a]]" {
		t.Errorf("agents %s", got)
	}
}

func TestComposeContextWithDrafts(t *testing.T) {
	cs := connectTo(t, composeFake(), "good", true)
	long := strings.Repeat("- a rule\n", repoBudget+1)
	out, text, isErr := call(t, cs, "aegis_compose_context", map[string]any{
		"repo":  "web",
		"role":  "be",
		"teams": []string{"pay"},
		"drafts": map[string]string{
			"repos/web/claude/CLAUDE.md":         long,
			"roles/be/claude/CLAUDE.md":          "- New role.\n",
			"global/claude/CLAUDE.md":            strings.Repeat("- g\n", globalBudget+1),
			"repos/web/claude/skills/x/SKILL.md": "",
		},
	})
	if isErr {
		t.Fatal(text)
	}
	secs := out["sections"].([]any)
	if len(secs) != 3 || secs[0].(map[string]any)["draft"] != true || secs[1].(map[string]any)["draft"] != nil {
		t.Errorf("sections %v", secs)
	}
	if secs[2].(map[string]any)["path"] != "roles/be/claude/CLAUDE.md" {
		t.Errorf("a new file's draft is not composed: %v", secs)
	}
	if out["over_budget"] != true || out["global_over_budget"] != true || out["notes"] != nil {
		t.Errorf("flags %v", out)
	}

	// An empty draft stands for a deletion: the section and the skill go.
	out, _, _ = call(t, cs, "aegis_compose_context", map[string]any{
		"repo": "web", "teams": []string{},
		"drafts": map[string]string{"repos/web/claude/CLAUDE.md": " ", "repos/web/claude/skills/deploy/SKILL.md": ""},
	})
	if len(out["sections"].([]any)) != 0 || out["claude_md"] != "" || fmt.Sprint(out["skills"]) != "map[]" {
		t.Errorf("deleted by draft: %v", out)
	}
}

func TestComposeContextEmptyOrg(t *testing.T) {
	cs := connectTo(t, routes{"GET /api/content/files": {200, `[]`}, "GET /api/repos": {200, webRegistered}}, "good", true)
	out, text, isErr := call(t, cs, "aegis_compose_context", map[string]any{"repo": "web"})
	if isErr || out["claude_md"] != "" || out["global"] != "" || out["notes"] != nil || len(out["repeated"].([]any)) != 0 || out["registered"] != true {
		t.Errorf("empty org: %v %s", out, text)
	}
}

func TestComposeContextUnregisteredRepo(t *testing.T) {
	cs := connectTo(t, routes{"GET /api/content/files": {200, `[]`}, "GET /api/repos": {200, `[{"repo":"other"},"junk"]`}}, "good", true)
	out, text, isErr := call(t, cs, "aegis_compose_context", map[string]any{"repo": "web", "teams": []string{}})
	if isErr || out["registered"] != false || !strings.Contains(fmt.Sprint(out["notes"]), "not a registered repository") {
		t.Errorf("unregistered: %v %s", out, text)
	}
}

func TestComposeContextErrors(t *testing.T) {
	for name, h := range map[string]routes{
		"list":   {},
		"repos":  {"GET /api/content/files": {200, `[]`}},
		"layer":  {"GET /api/content/files": {200, `["repos/web/claude/CLAUDE.md"]`}, "GET /api/repos": {200, webRegistered}},
		"global": {"GET /api/content/files": {200, `["global/claude/CLAUDE.md"]`}, "GET /api/repos": {200, webRegistered}},
	} {
		cs := connectTo(t, h, "good", true)
		if _, text, isErr := call(t, cs, "aegis_compose_context", map[string]any{"repo": "web"}); !isErr {
			t.Errorf("%s: no error: %s", name, text)
		}
	}

	cs := connectTo(t, composeFake(), "good", true)
	for _, args := range []map[string]any{
		{"repo": ""},
		{"repo": "a/b"},
		{"repo": "web", "role": ".."},
		{"repo": "web", "person": "."},
		{"repo": "web", "teams": []string{""}},
		{"repo": "web", "teams": []string{`x\y`}},
	} {
		if _, text, isErr := call(t, cs, "aegis_compose_context", args); !isErr {
			t.Errorf("%v accepted: %s", args, text)
		}
	}
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"":                      "",
		"   ":                   "",
		"# Heading":             "",
		"```bash":               "",
		"<!-- aegis:repo=x -->": "",
		"---":                   "",
		"| --- | --- |":         "",
		"- Run  the   Tests.":   "run the tests",
		"* run the tests":       "run the tests",
		"> run the tests":       "run the tests",
		"1. run the tests":      "run the tests",
		"12) run the tests":     "run the tests",
		"e.g. run the tests":    "e.g. run the tests",
	} {
		if got := normalize(in); got != want {
			t.Errorf("normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEntriesAndLayerNames(t *testing.T) {
	exists := map[string]bool{
		"r/claude/skills/a/SKILL.md":       true,
		"r/claude/skills/a/b/SKILL.md":     true,
		"r/claude/skills/gone/SKILL.md":    false,
		"r/claude/agents/.md":              true,
		"teams/t/claude/CLAUDE.md":         true,
		"teams/gone/claude/CLAUDE.md":      false,
		"teams/t/claude/skills/s/SKILL.md": true,
	}
	if got := entries(exists, "r/claude/skills/", "/SKILL.md"); !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("skills %v", got)
	}
	if got := entries(exists, "r/claude/agents/", ".md"); got != nil {
		t.Errorf("agents %v", got)
	}
	if got := layerNames(exists, "teams/"); !reflect.DeepEqual(got, []string{"t"}) {
		t.Errorf("teams %v", got)
	}
}

func TestLineCount(t *testing.T) {
	for in, want := range map[string]int{"": 0, "\n": 0, "a": 1, "a\nb\n": 2} {
		if got := lineCount(in); got != want {
			t.Errorf("lineCount(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestWritesReportMarkdownLines(t *testing.T) {
	cs, _ := connect(t, "good", false)
	out, text, _ := call(t, cs, "aegis_create_file", map[string]any{"plane": "content", "path": "teams/t/claude/CLAUDE.md", "body": "- a\n- b\n"})
	if out["lines"] != float64(2) {
		t.Errorf("create: %v %s", out, text)
	}
	out, text, _ = call(t, cs, "aegis_update_file", map[string]any{"plane": "content", "path": "teams/t/claude/CLAUDE.md", "body": "- a\n", "etag": etag("- a\n- b\n")})
	if out["lines"] != float64(1) {
		t.Errorf("update: %v %s", out, text)
	}
	out, text, _ = call(t, cs, "aegis_create_file", map[string]any{"plane": "config", "path": "global/claude/settings.json", "body": "{}\n"})
	if _, ok := out["lines"]; ok || out["created"] != true {
		t.Errorf("config: %v %s", out, text)
	}
}
