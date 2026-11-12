package tools

import (
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func promptNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListPrompts(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range res.Prompts {
		names = append(names, p.Name)
	}
	sort.Strings(names)
	return names
}

func getPrompt(t *testing.T, cs *mcp.ClientSession, name string, args map[string]string) (string, error) {
	t.Helper()
	res, err := cs.GetPrompt(t.Context(), &mcp.GetPromptParams{Name: name, Arguments: args})
	if err != nil {
		return "", err
	}
	return res.Messages[0].Content.(*mcp.TextContent).Text, nil
}

func TestPromptsFollowReadOnly(t *testing.T) {
	rw, _ := connect(t, "good", false)
	ro, _ := connect(t, "good", true)
	if got := strings.Join(promptNames(t, rw), " "); got != "add-guidance add-repo add-role add-team health-check" {
		t.Errorf("prompts %s", got)
	}
	if got := strings.Join(promptNames(t, ro), " "); got != "health-check" {
		t.Errorf("read-only prompts %s", got)
	}
}

func TestPromptText(t *testing.T) {
	cs, _ := connect(t, "good", false)
	for _, c := range []struct {
		name    string
		args    map[string]string
		want    []string
		notWant string
	}{
		{"add-repo", map[string]string{"repo": " web "}, []string{"repository web into", "aegis_compose_context", "ask me what you cannot tell", "Write nothing until I agree"}, ""},
		{"add-guidance", map[string]string{"guidance": "Use pnpm.", "repo": "web"}, []string{"\n\nUse pnpm.\n\n", "about the repository web", "subagent"}, ""},
		{"add-guidance", map[string]string{"guidance": "Use pnpm."}, []string{"Use pnpm."}, "about the repository"},
		{"health-check", map[string]string{"repo": "web"}, []string{"the repository web", "Change nothing", "Ask me which proposals"}, ""},
		{"health-check", nil, []string{"every registered repository"}, ""},
		{"add-team", map[string]string{"name": "pay", "purpose": "payments engineers"}, []string{"the team pay", "It is for: payments engineers", "never facts about one repository", "aegis_create_team_or_role", "administrator"}, ""},
		{"add-role", map[string]string{"name": "fe"}, []string{"the role fe", "MCP servers, skills and agents", "subagent"}, "It is for"},
	} {
		text, err := getPrompt(t, cs, c.name, c.args)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		for _, w := range c.want {
			if !strings.Contains(text, w) {
				t.Errorf("%s %v: missing %q in\n%s", c.name, c.args, w, text)
			}
		}
		if c.notWant != "" && strings.Contains(text, c.notWant) {
			t.Errorf("%s %v: has %q", c.name, c.args, c.notWant)
		}
	}
}

func TestPromptRequiresArguments(t *testing.T) {
	cs, _ := connect(t, "good", false)
	for _, name := range []string{"add-repo", "add-guidance", "add-team", "add-role"} {
		if _, err := getPrompt(t, cs, name, map[string]string{"repo": "  "}); err == nil || !strings.Contains(err.Error(), "required") {
			t.Errorf("%s without its argument: %v", name, err)
		}
	}
}
