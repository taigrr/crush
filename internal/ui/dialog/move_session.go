package dialog

import (
	"cmp"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/progress"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/taigrr/crush/internal/cluster"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/transfer"
	"github.com/taigrr/crush/internal/ui/common"
)

// MoveSessionID is the identifier for the move-session dialog.
const MoveSessionID = "move_session"

// MoveTarget identifies the session a move dialog acts on.
type MoveTarget struct {
	// Root is the session's workspace root as the sidebar knows it (""
	// for the current workspace).
	Root      string
	SessionID string
	Title     string
	// OwnerNode is the node the session lives on ("" = home).
	OwnerNode string
}

// MoveSession picks a destination node for a session, then — if the
// destination has diverging files — how to resolve the conflict.
type MoveSession struct {
	com    *common.Common
	target MoveTarget
	nodes  []cluster.NodeDashboard
	cursor int
	// conflict, when set, switches the dialog to the conflict prompt for
	// the chosen node.
	conflict     string
	conflictNode cluster.NodeDashboard
	// confirm, when set, asks whether to clean up snapshots before
	// moving to the chosen node.
	confirm *cluster.NodeDashboard
	// gc is the user's cleanup choice, kept for conflict retries.
	gc  bool
	err string
	// moving is set once a move started; progress then replaces the list.
	moving   bool
	progress proto.TransferStatus
	keyMap   struct {
		Up, Down, Select, Backup, Keep, GC, Close key.Binding
	}
}

var _ Dialog = (*MoveSession)(nil)

// NewMoveSession creates the dialog. selfID is the attached server's node
// ID, used to resolve OwnerNode "" (home).
func NewMoveSession(com *common.Common, target MoveTarget, rows []cluster.NodeDashboard, selfID string) *MoveSession {
	owner := target.OwnerNode
	if owner == "" {
		owner = selfID
	}
	d := &MoveSession{com: com, target: target}
	for _, r := range rows {
		if r.Member.NodeID != owner {
			d.nodes = append(d.nodes, r)
		}
	}
	// Default to the online node with the most spare capacity.
	best := -1.0
	for i, r := range d.nodes {
		if r.Online && r.Stats != nil && r.Stats.FreeScore() > best {
			best, d.cursor = r.Stats.FreeScore(), i
		}
	}
	d.keyMap.Up = key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up"))
	d.keyMap.Down = key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down"))
	d.keyMap.Select = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "move"))
	d.keyMap.Backup = key.NewBinding(key.WithKeys("b"), key.WithHelp("b", "back up & replace"))
	d.keyMap.Keep = key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "conversation only"))
	d.keyMap.GC = key.NewBinding(key.WithKeys("g"), key.WithHelp("g", "clean up, then move"))
	d.keyMap.Close = CloseKey
	return d
}

// ID implements [Dialog].
func (*MoveSession) ID() string { return MoveSessionID }

// ShowConflict switches to the conflict prompt after a refused move.
func (d *MoveSession) ShowConflict(node cluster.NodeDashboard, reason string) {
	d.conflict, d.conflictNode, d.moving = reason, node, false
}

// ShowError displays a failure without closing.
func (d *MoveSession) ShowError(err string) { d.err, d.moving = err, false }

// SetProgress shows live transfer progress for the running move.
func (d *MoveSession) SetProgress(s proto.TransferStatus) {
	if s.SessionID == d.target.SessionID {
		d.progress = s
	}
}

// HandleMsg implements [Dialog].
func (d *MoveSession) HandleMsg(msg tea.Msg) Action {
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	if key.Matches(kp, d.keyMap.Close) {
		return ActionClose{}
	}
	if d.moving {
		return nil
	}
	if d.conflict != "" {
		switch {
		case key.Matches(kp, d.keyMap.Backup):
			return d.action(d.conflictNode, transfer.ConflictBackup)
		case key.Matches(kp, d.keyMap.Keep):
			return d.action(d.conflictNode, transfer.ConflictKeep)
		}
		return nil
	}
	if d.confirm != nil {
		switch {
		case key.Matches(kp, d.keyMap.GC):
			d.gc = true
			return d.action(*d.confirm, transfer.ConflictRefuse)
		case key.Matches(kp, d.keyMap.Select):
			d.gc = false
			return d.action(*d.confirm, transfer.ConflictRefuse)
		}
		return nil
	}
	switch {
	case key.Matches(kp, d.keyMap.Up):
		d.cursor = max(0, d.cursor-1)
	case key.Matches(kp, d.keyMap.Down):
		d.cursor = min(len(d.nodes)-1, d.cursor+1)
	case key.Matches(kp, d.keyMap.Select):
		if d.cursor < len(d.nodes) {
			n := d.nodes[d.cursor]
			if !n.Online {
				d.err = n.Member.Name + " is offline"
				return nil
			}
			d.err = ""
			d.confirm = &n
			return nil
		}
	}
	return nil
}

func (d *MoveSession) action(n cluster.NodeDashboard, policy transfer.ConflictPolicy) Action {
	d.err, d.conflict, d.confirm, d.moving = "", "", nil, true
	d.progress = proto.TransferStatus{Stage: "starting", TargetName: n.Member.Name}
	return ActionMoveSession{Target: d.target, Node: n, OnConflict: policy, GC: d.gc}
}

// Draw implements [Dialog].
func (d *MoveSession) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := d.com.Styles
	width := min(max(50, area.Dx()-10), 90)
	inner := width - 4
	title := d.target.Title
	if title == "" {
		title = "session"
	}
	lines := []string{t.Dialog.Title.Render("Move " + ansi.Truncate(title, inner-6, "…")), ""}
	switch {
	case d.moving:
		bar := progress.New(progress.WithColors(t.WorkingGradFromColor, t.WorkingGradToColor), progress.WithoutPercentage())
		bar.SetWidth(inner)
		p := d.progress
		pct := 0.0
		if p.TotalBytes > 0 {
			pct = min(1, float64(p.Bytes)/float64(p.TotalBytes))
		}
		stage := p.Stage
		if p.TotalFiles > 0 {
			stage = fmt.Sprintf("%s · %d/%d files · %.0f%%", p.Stage, p.Files, p.TotalFiles, pct*100)
		}
		lines = append(lines, "Moving to "+cmp.Or(p.TargetName, "destination")+"…",
			t.Resource.AdditionalText.Render(ansi.Truncate(stage, inner, "…")), "", bar.ViewAs(pct))
	case d.confirm != nil:
		lines = append(lines,
			"Clean up Crush snapshots before moving to "+d.confirm.Member.Name+"?",
			t.Resource.AdditionalText.Render("Garbage-collects .crush/git so unreachable snapshot data is not sent. It can take a while on large projects."),
			"",
			"g      clean up, then move",
			"enter  move now",
			"esc    cancel",
		)
	case d.conflict != "":
		lines = append(lines,
			fmt.Sprintf("%s already has different files at this path:", d.conflictNode.Member.Name),
			t.Resource.AdditionalText.Render(ansi.Truncate(d.conflict, inner, "…")),
			"",
			"b  back them up (renamed aside) and send this project",
			"c  move only the conversation; keep their files",
			"esc  cancel",
		)
	case len(d.nodes) == 0:
		lines = append(lines, "No other machines in the cluster. Pair one with `crush cluster join`.")
	default:
		for i, n := range d.nodes {
			dot := t.Resource.OnlineIcon.String()
			if !n.Online {
				dot = t.Resource.ErrorIcon.String()
			}
			info := "offline"
			if n.Stats != nil && n.Online {
				info = fmt.Sprintf("%3.0f%% free · CPU %3.0f%% · MEM %3.0f%% · %d jobs", n.Stats.FreeScore()*100, n.Stats.CPUPercent, n.Stats.MemPercent, len(n.Stats.Jobs))
			}
			row := fmt.Sprintf("%s %-14s %s", dot, ansi.Truncate(n.Member.Name, 14, "…"), t.Resource.AdditionalText.Render(info))
			if i == d.cursor {
				row = t.Dialog.SelectedItem.UnsetPadding().Width(inner).Render(ansi.Strip(row))
			}
			lines = append(lines, row)
		}
		lines = append(lines, "", t.Resource.AdditionalText.Render("History and project files (uncommitted too) travel together."))
	}
	if d.err != "" {
		lines = append(lines, "", t.Resource.ErrorIcon.Render(ansi.Wordwrap(d.err, inner, " /:")))
	}
	help := "↑/↓ choose · enter move · esc cancel"
	if d.moving {
		help = "moving… esc hides this; progress continues in the sidebar"
	}
	if d.conflict != "" {
		help = "b back up · c conversation only · esc cancel"
	}
	if d.confirm != nil {
		help = "g clean up & move · enter move now · esc cancel"
	}
	lines = append(lines, "", t.Resource.AdditionalText.Render(help))
	view := t.Dialog.View.Width(width).Render(lipgloss.JoinVertical(lipgloss.Left, strings.Join(lines, "\n")))
	DrawCenter(scr, area, view)
	return nil
}

// ShortHelp implements [help.KeyMap].
func (d *MoveSession) ShortHelp() []key.Binding {
	if d.conflict != "" {
		return []key.Binding{d.keyMap.Backup, d.keyMap.Keep, d.keyMap.Close}
	}
	if d.confirm != nil {
		return []key.Binding{d.keyMap.GC, d.keyMap.Select, d.keyMap.Close}
	}
	return []key.Binding{d.keyMap.Up, d.keyMap.Down, d.keyMap.Select, d.keyMap.Close}
}

// FullHelp implements [help.KeyMap].
func (d *MoveSession) FullHelp() [][]key.Binding { return [][]key.Binding{d.ShortHelp()} }
