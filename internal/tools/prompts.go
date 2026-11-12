package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A prompt is a workflow the user starts by name. Each one leans on the
// placement ladder and the review step in Instructions rather than repeating
// them, so the two must agree.
type prompt struct {
	p    *mcp.Prompt
	text func(args map[string]string) string
}

func addPrompt(s *mcp.Server, pr prompt) {
	s.AddPrompt(pr.p, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		args := map[string]string{}
		for k, v := range req.Params.Arguments {
			args[k] = strings.TrimSpace(v)
		}
		for _, a := range pr.p.Arguments {
			if a.Required && args[a.Name] == "" {
				return nil, fmt.Errorf("%s is required", a.Name)
			}
		}
		return &mcp.GetPromptResult{
			Description: pr.p.Description,
			Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: pr.text(args)}}},
		}, nil
	})
}

// reviewStep is the delegated "what developers receive" check, shared by the
// prompts that write.
const reviewStep = "Have a subagent check the drafts with aegis_compose_context (pass them as drafts, once per role with files in the repository) and report back only: total lines against budget, the largest sections, repeated or near-repeated lines, contradictions, and lines in the wrong layer or that belong in a skill. Fix what it finds, and tell me what it found."

var addRepoPrompt = prompt{
	p: &mcp.Prompt{
		Name:        "add-repo",
		Title:       "Add a repository",
		Description: "Bring a repository into Aegis: its existing AI instructions, skills and agents, sorted into the right layers instead of copied as one long file.",
		Arguments: []*mcp.PromptArgument{
			{Name: "repo", Description: "the repository's slug or git remote", Required: true},
		},
	},
	text: func(a map[string]string) string {
		return "Help me bring the repository " + a["repo"] + " into Aegis, following this server's instructions. Walk me through it and explain each step as you go.\n" +
			"1. Call aegis_whoami and aegis_list_repos. If it is not registered, explain what registering does, propose a slug and ask me before registering it.\n" +
			"2. Call aegis_scan_repo_import, and read what Aegis already holds for this repository, the roles, the teams and global. Tell me briefly what you found.\n" +
			"3. Before sorting anything, ask me what you cannot tell from the files: which teams and roles work in this repository, and whether any guidance should reach people beyond it. Offer options where you can.\n" +
			"4. Sort every rule with the placement ladder: cut it, enforce it in settings, make it a skill or agent, or put it in the narrowest layer. Use aegis_import_repo only for files that belong in the repository layer unchanged; draft the rest.\n" +
			"5. " + reviewStep + "\n" +
			"6. Show me the plan: each file, its layer and who that reaches, why, what you would cut, and the sizes before and after. Write nothing until I agree, and change the plan if I ask."
	},
}

var addGuidancePrompt = prompt{
	p: &mcp.Prompt{
		Name:        "add-guidance",
		Title:       "Add guidance",
		Description: "Add a rule, convention or preference to Aegis. Works out who should receive it and where it belongs (or that it needs no line at all), without duplicating what is there.",
		Arguments: []*mcp.PromptArgument{
			{Name: "guidance", Description: "the rule, convention or preference to add", Required: true},
			{Name: "repo", Description: "the repository it is about, if any"},
		},
	},
	text: func(a map[string]string) string {
		scope := ""
		if a["repo"] != "" {
			scope = " It is about the repository " + a["repo"] + "."
		}
		return "Help me add this guidance to Aegis, following this server's instructions." + scope + "\n\n" + a["guidance"] + "\n\n" +
			"1. Make sure you understand who it is for: everyone, a team, a role, one repository, or just me. If that is not clear, ask, offering the options that fit with your recommendation.\n" +
			"2. Run it through the placement ladder and explain where it should go and why, including when the answer is to write nothing or to use a setting or skill instead.\n" +
			"3. Read the target file and the other layers that land in the same file. Fold it into what is there; never add a second copy of a rule.\n" +
			"4. " + reviewStep + " Without a repository, check one that the layer reaches.\n" +
			"5. Show me the change, who receives it and the sizes before and after. Write only after I agree."
	},
}

var (
	addTeamPrompt = layerPrompt("team", "payments", "Add a team",
		"Create a team in Aegis: a group of people whose shared way of working follows them into every repository.",
		"how this group of people works in any repository (review, tickets, release habits), never facts about one repository")
	addRolePrompt = layerPrompt("role", "backend", "Add a role",
		"Create a role in Aegis: a job function, with the instructions, settings, MCP servers, skills and agents everyone in it receives.",
		"how this job function works across repositories, plus the settings, MCP servers, skills and agents it needs. What it does differently in one repository goes in repos/<repo>/roles/<role>")
)

// layerPrompt is the walkthrough for creating a team or a role.
func layerPrompt(kind, example, title, description, holds string) prompt {
	return prompt{
		p: &mcp.Prompt{
			Name:        "add-" + kind,
			Title:       title,
			Description: description,
			Arguments: []*mcp.PromptArgument{
				{Name: "name", Description: "the " + kind + "'s name, e.g. " + example, Required: true},
				{Name: "purpose", Description: "what the " + kind + " is for, if you want to say up front"},
			},
		},
		text: func(a map[string]string) string {
			purpose := ""
			if a["purpose"] != "" {
				purpose = " It is for: " + a["purpose"]
			}
			return "Help me add the " + kind + " " + a["name"] + " to Aegis, following this server's instructions. Explain each step as you go." + purpose + "\n" +
				"1. Call aegis_whoami and aegis_list_teams_and_roles. If this name or a similar one exists, show me and ask whether to use that instead. Suggest a lower-case, hyphenated name if mine does not fit.\n" +
				"2. Ask me what you cannot tell: what the " + kind + " is for, who is in it, and which repositories they work in. Offer options with your recommendation.\n" +
				"3. Decide with the placement ladder what its layer holds: " + holds + ". Look through global, the other teams and roles, and those repositories for lines that belong here instead, and propose moving them. A short or empty layer is fine.\n" +
				"4. " + reviewStep + " Check one of those repositories, with the " + kind + "'s CLAUDE.md as a draft.\n" +
				"5. Show me the plan, with sizes before and after. Only after I agree, create it with aegis_create_team_or_role and make the other changes.\n" +
				"6. Tell me it reaches nobody until an administrator assigns people to it in the Aegis admin UI."
		},
	}
}

var healthCheckPrompt = prompt{
	p: &mcp.Prompt{
		Name:        "health-check",
		Title:       "Health check",
		Description: "Check what developers receive from Aegis for length, repetition and guidance in the wrong place, explain what it means, and suggest fixes. Changes nothing on its own.",
		Arguments: []*mcp.PromptArgument{
			{Name: "repo", Description: "one repository's slug; leave out to check every registered repository"},
		},
	},
	text: func(a map[string]string) string {
		scope := "every registered repository"
		if a["repo"] != "" {
			scope = "the repository " + a["repo"]
		}
		return "Give me a health check of what developers receive from Aegis in " + scope + ", following this server's instructions. Change nothing.\n" +
			"1. Call aegis_list_repos and aegis_list_files for content, and tell me briefly what is set up: repositories, teams, roles, and which layers are empty.\n" +
			"2. For each repository and each role with files there, have a subagent call aegis_compose_context and report back only: total lines against budget, the largest sections, repeated or near-repeated lines, contradictions, lines in the wrong layer, and procedures that belong in a skill. Run them in parallel if you can.\n" +
			"3. Explain the findings in plain words, ranked by how many developers they reach: global first, then teams and roles, then repositories. For each, say why it matters and propose the move or cut, with sizes before and after.\n" +
			"4. Ask me which proposals to apply, and apply only those, one confirmed plan at a time."
	},
}
