package model

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/ui/completions"
	"github.com/taigrr/crush/internal/ui/dialog"
	"github.com/taigrr/crush/internal/ui/util"
)

// handleModelSlash implements the /model command. It edits the workspace's
// model roles in config through the same path the model picker uses, so it
// needs no session state:
//
//	/model                        show the model this session runs and every role
//	/model <model> [effort]       set the large model (what you talk to)
//	/model <role>                 show what <role> is set to
//	/model <role> <model> [effort]
//	                              set <role>: large, small, worker, or a custom
//	                              role name (created if new)
//
// <model> is resolved with config.ResolveModelRefLoose, so a substring of
// the id or display name is enough ("fable", "haiku", "fable xhigh").
func (m *UI) handleModelSlash(args string) tea.Cmd {
	cfg := m.com.Config()
	if cfg == nil {
		return util.ReportError(errors.New("configuration not found"))
	}
	args = strings.TrimSpace(args)
	if args == "" {
		return util.ReportInfo(m.describeModels())
	}

	action, err := m.parseModelSlash(cfg, args)
	if err != nil {
		return util.ReportError(err)
	}
	if action.show {
		sel, ok := cfg.Models[action.role]
		if !ok {
			return util.ReportInfo(fmt.Sprintf("%s is not set", action.role))
		}
		return util.ReportInfo(fmt.Sprintf("%s = %s", action.role, describeSelection(sel)))
	}
	return m.applyRoleFromSlash(action.role, action.sel)
}

// modelSlashAction is the parsed form of a /model argument string: either
// show a role, or set role = sel.
type modelSlashAction struct {
	role config.SelectedModelType
	sel  config.SelectedModel
	show bool
}

// parseModelSlash turns the argument string into an action:
//
//	<role>                    show role
//	<role> <model> [effort]   set role (existing role, builtin or custom)
//	<model> [effort]          set large
//	<new-role> <model> [effort]
//	                          create a custom role, only when the whole
//	                          string does not itself resolve as a model
//	                          (so a multi-word model reference is never
//	                          mistaken for a role) and the first word is a
//	                          valid role name.
func (m *UI) parseModelSlash(cfg *config.Config, args string) (modelSlashAction, error) {
	fields := strings.Fields(args)
	if role, isRole := m.roleToken(fields[0]); isRole {
		if len(fields) == 1 {
			return modelSlashAction{role: role, show: true}, nil
		}
		sel, err := cfg.ResolveModelRefLoose(strings.Join(fields[1:], " "))
		if err != nil {
			return modelSlashAction{}, err
		}
		return modelSlashAction{role: role, sel: sel}, nil
	}

	sel, err := cfg.ResolveModelRefLoose(args)
	if err == nil {
		return modelSlashAction{role: config.SelectedModelTypeLarge, sel: sel}, nil
	}
	if len(fields) >= 2 && validRoleName(fields[0]) {
		if roleSel, rerr := cfg.ResolveModelRefLoose(strings.Join(fields[1:], " ")); rerr == nil {
			return modelSlashAction{role: config.SelectedModelType(fields[0]), sel: roleSel}, nil
		}
	}
	return modelSlashAction{}, err
}

// applyRoleFromSlash writes role = sel to config through the model picker's
// path (auth prompts, small-model defaulting, agent refresh all included).
func (m *UI) applyRoleFromSlash(role config.SelectedModelType, sel config.SelectedModel) tea.Cmd {
	if !validRoleName(string(role)) {
		return util.ReportError(fmt.Errorf("invalid role name %q: use letters, digits, '-' or '_'", role))
	}
	// Match what the model picker writes: a bare or fuzzy reference
	// resolves to provider/model only, and an empty effort would leave
	// reasoning off while the UI shows the default level as active.
	if cw := m.com.Config().GetModel(sel.Provider, sel.Model); cw != nil {
		if sel.ReasoningEffort == "" {
			sel.ReasoningEffort = cw.DefaultReasoningEffort
		}
		if sel.MaxTokens == 0 {
			sel.MaxTokens = cw.DefaultMaxTokens
		}
	}
	return m.handleSelectModel(dialog.ActionSelectModel{
		Provider:  m.catwalkProvider(sel.Provider),
		Model:     sel,
		ModelType: role,
	})
}

// validRoleName restricts role names to characters that are safe as a
// config JSON key path segment ("models.<role>").
func validRoleName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// roleToken reports whether tok names a model role: a builtin slot or any
// user-defined key under models (matched case-insensitively, returning the
// configured spelling).
func (m *UI) roleToken(tok string) (config.SelectedModelType, bool) {
	role := config.SelectedModelType(strings.ToLower(tok))
	if config.IsBuiltinModelType(role) {
		return role, true
	}
	if cfg := m.com.Config(); cfg != nil {
		for r := range cfg.Models {
			if strings.EqualFold(string(r), tok) {
				return r, true
			}
		}
	}
	return "", false
}

// catwalkProvider returns the catalog entry for id, or a stub carrying just
// the id when the provider is custom. handleSelectModel only consults it
// when the provider is not configured, which cannot be the case for a
// selection that came out of the resolver.
func (m *UI) catwalkProvider(id string) catwalk.Provider {
	if providers, err := config.Providers(m.com.Config()); err == nil {
		for _, p := range providers {
			if string(p.ID) == id {
				return p
			}
		}
	}
	return catwalk.Provider{ID: catwalk.InferenceProvider(id), Name: id}
}

func describeSelection(sel config.SelectedModel) string {
	s := sel.Provider + "/" + sel.Model
	if sel.ReasoningEffort != "" {
		s += " (" + sel.ReasoningEffort + ")"
	}
	return s
}

// describeModels renders the session's effective model and the role table.
func (m *UI) describeModels() string {
	cfg := m.com.Config()
	var b strings.Builder
	large, hasLarge := cfg.Models[config.SelectedModelTypeLarge]
	switch {
	case m.session != nil && m.session.ModelRef != "":
		if eff := m.effectiveModel(); eff != nil && eff.ModelCfg.Model != large.Model {
			fmt.Fprintf(&b, "this session: %s (spawned with model %q)\n", describeSelection(eff.ModelCfg), m.session.ModelRef)
		} else {
			fmt.Fprintf(&b, "this session: large (spawned with model %q, which no longer resolves)\n", m.session.ModelRef)
		}
	case hasLarge:
		fmt.Fprintf(&b, "this session: %s (large)\n", describeSelection(large))
	}
	roles := make([]string, 0, len(cfg.Models))
	for r := range cfg.Models {
		roles = append(roles, string(r))
	}
	sort.Slice(roles, func(i, j int) bool { return roleOrder(roles[i]) < roleOrder(roles[j]) })
	for _, r := range roles {
		fmt.Fprintf(&b, "%s: %s\n", r, describeSelection(cfg.Models[config.SelectedModelType(r)]))
	}
	return strings.TrimRight(b.String(), "\n")
}

func roleOrder(r string) string {
	switch config.SelectedModelType(r) {
	case config.SelectedModelTypeLarge:
		return "0"
	case config.SelectedModelTypeSmall:
		return "1"
	case config.SelectedModelTypeWorker:
		return "2"
	}
	return "3" + r
}

// modelArgCompletions supplies completions for "/model ". With no argument
// yet it offers the roles followed by every enabled model; once a role has
// been typed it offers only models (plus effort levels once a model has
// been chosen).
func (m *UI) modelArgCompletions(args string) []completions.ArgCompletionValue {
	cfg := m.com.Config()
	if cfg == nil {
		return nil
	}
	fields := strings.Fields(args)

	var out []completions.ArgCompletionValue
	if len(fields) == 0 {
		for _, r := range []config.SelectedModelType{config.SelectedModelTypeLarge, config.SelectedModelTypeSmall, config.SelectedModelTypeWorker} {
			desc := "role: " + roleDescription(r)
			if sel, ok := cfg.Models[r]; ok {
				desc += " — now " + sel.Model
			}
			out = append(out, completions.ArgCompletionValue{Text: string(r), Description: desc, Continue: true})
		}
		custom := make([]string, 0)
		for r := range cfg.Models {
			if !config.IsBuiltinModelType(r) {
				custom = append(custom, string(r))
			}
		}
		sort.Strings(custom)
		for _, r := range custom {
			out = append(out, completions.ArgCompletionValue{Text: r, Description: "role — now " + cfg.Models[config.SelectedModelType(r)].Model, Continue: true})
		}
		return append(out, m.modelCompletions()...)
	}

	// A role followed by nothing: offer models.
	if _, isRole := m.roleToken(fields[0]); isRole && len(fields) == 1 {
		return m.modelCompletions()
	}

	// Something that resolves to a model was typed: offer its effort levels,
	// unless one was already given.
	if slices.Contains(config.ReasoningEffortLevels, strings.ToLower(fields[len(fields)-1])) {
		return nil
	}
	ref := args
	if _, isRole := m.roleToken(fields[0]); isRole {
		ref = strings.Join(fields[1:], " ")
	}
	if sel, err := cfg.ResolveModelRefLoose(ref); err == nil {
		if cw := cfg.GetModel(sel.Provider, sel.Model); cw != nil {
			for _, lvl := range cw.ReasoningLevels {
				desc := "reasoning effort"
				if lvl == cw.DefaultReasoningEffort {
					desc += " (default)"
				}
				out = append(out, completions.ArgCompletionValue{Text: lvl, Description: desc})
			}
		}
	}
	return out
}

func roleDescription(r config.SelectedModelType) string {
	switch r {
	case config.SelectedModelTypeLarge:
		return "the model you talk to"
	case config.SelectedModelTypeSmall:
		return "titles and summaries"
	case config.SelectedModelTypeWorker:
		return "default for agent/review sub-tasks"
	}
	return ""
}

// modelCompletions lists every model from every enabled provider as
// provider/id, with the display name as description. Models currently
// holding a role are listed first.
func (m *UI) modelCompletions() []completions.ArgCompletionValue {
	cfg := m.com.Config()
	if cfg == nil || cfg.Providers == nil {
		return nil
	}
	roleNames := make([]string, 0, len(cfg.Models))
	for r := range cfg.Models {
		roleNames = append(roleNames, string(r))
	}
	sort.Slice(roleNames, func(i, j int) bool { return roleOrder(roleNames[i]) < roleOrder(roleNames[j]) })
	inRole := map[string]string{}
	for _, r := range roleNames {
		sel := cfg.Models[config.SelectedModelType(r)]
		key := sel.Provider + "/" + sel.Model
		if inRole[key] != "" {
			inRole[key] += ","
		}
		inRole[key] += r
	}
	type row struct {
		text, desc string
		role       bool
	}
	var rows []row
	for providerID, p := range cfg.Providers.Seq2() {
		if p.Disable {
			continue
		}
		for _, mdl := range p.Models {
			key := providerID + "/" + mdl.ID
			desc := mdl.Name
			if roles := inRole[key]; roles != "" {
				desc += " [" + roles + "]"
			}
			rows = append(rows, row{text: key, desc: desc, role: inRole[key] != ""})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].role != rows[j].role {
			return rows[i].role
		}
		return rows[i].text < rows[j].text
	})
	out := make([]completions.ArgCompletionValue, 0, len(rows))
	for _, r := range rows {
		out = append(out, completions.ArgCompletionValue{Text: r.text, Description: r.desc})
	}
	return out
}
