package dialog

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/taigrr/crush/internal/home"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/ui/common"
)

// MigrateID is the identifier for the migrate-all dialog.
const MigrateID = "migrate_all"

// Migrate previews and runs "move every job off this machine".
type Migrate struct {
	com     *common.Common
	plan    proto.MigrationPlan
	all     bool
	running bool
	result  *proto.MigrationResult
	err     string
	scroll  int
	keyMap  struct {
		Confirm, Strategy, All, Up, Down, Close key.Binding
	}
}

var _ Dialog = (*Migrate)(nil)

// NewMigrate creates the dialog for plan.
func NewMigrate(com *common.Common, plan proto.MigrationPlan, all bool) *Migrate {
	d := &Migrate{com: com, plan: plan, all: all}
	d.keyMap.Confirm = key.NewBinding(key.WithKeys("enter", "y"), key.WithHelp("enter", "migrate"))
	d.keyMap.Strategy = key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "capacity/even"))
	d.keyMap.All = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "include idle"))
	d.keyMap.Up = key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑", "scroll"))
	d.keyMap.Down = key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓", "scroll"))
	d.keyMap.Close = CloseKey
	return d
}

// ID implements [Dialog].
func (*Migrate) ID() string { return MigrateID }

// SetPlan replaces the previewed plan.
func (d *Migrate) SetPlan(plan proto.MigrationPlan, all bool) {
	d.plan, d.all, d.err, d.scroll = plan, all, "", 0
}

// SetResult shows the outcome of a run.
func (d *Migrate) SetResult(res proto.MigrationResult, err error) {
	d.running = false
	if err != nil {
		d.err = err.Error()
		return
	}
	d.result = &res
}

// HandleMsg implements [Dialog].
func (d *Migrate) HandleMsg(msg tea.Msg) Action {
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	if key.Matches(kp, d.keyMap.Close) && !d.running {
		return ActionClose{}
	}
	switch {
	case key.Matches(kp, d.keyMap.Up):
		d.scroll = max(0, d.scroll-1)
		return nil
	case key.Matches(kp, d.keyMap.Down):
		d.scroll++
		return nil
	}
	if d.running || d.result != nil {
		return nil
	}
	switch {
	case key.Matches(kp, d.keyMap.Confirm):
		if len(d.plan.Moves) == 0 {
			return nil
		}
		d.running = true
		return ActionMigrateExecute{Plan: d.plan}
	case key.Matches(kp, d.keyMap.Strategy):
		next := "even"
		if d.plan.Strategy == "even" {
			next = "capacity"
		}
		return ActionMigratePlan{Strategy: next, All: d.all}
	case key.Matches(kp, d.keyMap.All):
		return ActionMigratePlan{Strategy: d.plan.Strategy, All: !d.all}
	}
	return nil
}

// Draw implements [Dialog].
func (d *Migrate) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := d.com.Styles
	width := min(max(60, area.Dx()-10), 110)
	inner := width - 4
	muted := t.Resource.AdditionalText
	lines := []string{t.Dialog.Title.Render("Migrate jobs to the cluster"), ""}
	switch {
	case d.result != nil:
		for _, o := range d.result.Outcomes {
			mark := t.Resource.OnlineIcon.Render("✓")
			note := ""
			if o.Error != "" {
				mark, note = t.Resource.ErrorIcon.Render("✗"), " — "+o.Error
			}
			lines = append(lines, ansi.Truncate(fmt.Sprintf("%s %s → %s%s", mark, o.Title, o.TargetName, note), inner, "…"))
		}
		lines = append(lines, "", fmt.Sprintf("Migrated %d of %d sessions. They keep running on their new machines.", d.result.Moved, len(d.result.Outcomes)))
	case len(d.plan.Moves) == 0:
		what := "running jobs"
		if d.all {
			what = "sessions"
		}
		lines = append(lines, "No "+what+" to migrate.")
	default:
		scope := "running jobs"
		if d.all {
			scope = "all open sessions"
		}
		lines = append(lines, muted.Render(fmt.Sprintf("%d %s · placement: %s", len(d.plan.Moves), scope, d.plan.Strategy)), "")
		for _, m := range d.plan.Moves {
			state := muted.Render("idle   ")
			if m.Busy {
				state = t.Resource.BusyIcon.Render("running")
			}
			title := m.Title
			if title == "" {
				title = m.SessionID[:min(8, len(m.SessionID))]
			}
			lines = append(lines, ansi.Truncate(fmt.Sprintf("%s  %-28s %s → %s", state, ansi.Truncate(title, 28, "…"), muted.Render(home.Short(m.Root)), m.TargetName), inner, "…"))
		}
		lines = append(lines, "", muted.Render("Running turns pause, move with their project files, and resume there."))
	}
	if d.running {
		lines = append(lines, "", "Migrating…")
	}
	if d.err != "" {
		lines = append(lines, "", t.Resource.ErrorIcon.Render(ansi.Truncate(d.err, inner, "…")))
	}
	help := "enter migrate · s capacity/even · a include idle · esc cancel"
	if d.result != nil {
		help = "esc close"
	}
	lines = append(lines, "", muted.Render(help))
	maxBody := max(6, area.Dy()-6)
	if len(lines) > maxBody {
		d.scroll = min(d.scroll, len(lines)-maxBody)
		lines = lines[d.scroll : d.scroll+maxBody]
	}
	DrawCenter(scr, area, t.Dialog.View.Width(width).Render(strings.Join(lines, "\n")))
	return nil
}

// ShortHelp implements [help.KeyMap].
func (d *Migrate) ShortHelp() []key.Binding {
	return []key.Binding{d.keyMap.Confirm, d.keyMap.Strategy, d.keyMap.All, d.keyMap.Close}
}

// FullHelp implements [help.KeyMap].
func (d *Migrate) FullHelp() [][]key.Binding { return [][]key.Binding{d.ShortHelp()} }
