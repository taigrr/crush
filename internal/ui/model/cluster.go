package model

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/taigrr/crush/internal/cluster"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/ui/dialog"
	"github.com/taigrr/crush/internal/ui/notification"
	"github.com/taigrr/crush/internal/ui/util"
	"github.com/taigrr/crush/internal/workspace"
)

// handleClusterJoinAttention prompts for approval when another machine
// asks to join the cluster. An event without a request ID means the
// request was decided elsewhere, so any open prompt is dismissed.
func (m *UI) handleClusterJoinAttention(ev proto.AttentionEvent) tea.Cmd {
	if ev.ToolCallID == "" {
		m.dialog.CloseDialog(dialog.ClusterJoinID)
		return nil
	}
	if m.dialog.ContainsDialog(dialog.ClusterJoinID) {
		m.dialog.BringToFront(dialog.ClusterJoinID)
		return nil
	}
	m.dialog.OpenDialog(dialog.NewClusterJoin(m.com, ev.ToolCallID, ev.Detail, ev.NodeID))
	return m.sendNotification(notification.Notification{
		Title:   "Crush cluster",
		Message: fmt.Sprintf("%s wants to join your cluster.", ev.Detail),
	})
}

// decideClusterJoin sends the user's approve/deny choice to the server.
func (m *UI) decideClusterJoin(d dialog.ActionClusterJoinDecision) tea.Cmd {
	ws := m.com.Workspace
	return func() tea.Msg {
		ctx := context.Background()
		if d.Approve {
			if err := ws.ClusterApprove(ctx, d.RecordedOn, d.RequestID); err != nil {
				return util.InfoMsg{Type: util.InfoTypeError, Msg: fmt.Sprintf("Approve %s: %v", d.NodeName, err)}
			}
			return util.InfoMsg{Type: util.InfoTypeSuccess, Msg: fmt.Sprintf("%s joined the cluster.", d.NodeName)}
		}
		if err := ws.ClusterDeny(ctx, d.RecordedOn, d.RequestID); err != nil {
			return util.InfoMsg{Type: util.InfoTypeError, Msg: fmt.Sprintf("Deny %s: %v", d.NodeName, err)}
		}
		return util.InfoMsg{Type: util.InfoTypeInfo, Msg: fmt.Sprintf("Denied %s.", d.NodeName)}
	}
}

// clusterDashboardInterval is how often the open dashboard refreshes.
const clusterDashboardInterval = 2 * time.Second

type (
	clusterDashboardMsg struct {
		rows []cluster.NodeDashboard
		err  error
	}
	clusterDashboardTickMsg struct{}
)

// openClusterDashboard opens the dashboard and starts its refresh loop.
func (m *UI) openClusterDashboard() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.ClusterDashboardID) {
		m.dialog.BringToFront(dialog.ClusterDashboardID)
		return nil
	}
	m.dialog.OpenDialog(dialog.NewClusterDashboard(m.com))
	return m.fetchClusterDashboard()
}

func (m *UI) fetchClusterDashboard() tea.Cmd {
	ws := m.com.Workspace
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		rows, err := ws.ClusterDashboard(ctx)
		return clusterDashboardMsg{rows: rows, err: err}
	}
}

// handleClusterDashboard feeds fresh data to the open dashboard and
// schedules the next refresh. Closing the dialog ends the loop.
func (m *UI) handleClusterDashboard(msg clusterDashboardMsg) tea.Cmd {
	d, ok := m.dialog.Dialog(dialog.ClusterDashboardID).(*dialog.ClusterDashboard)
	if !ok {
		return nil
	}
	errStr := ""
	if msg.err != nil {
		errStr = msg.err.Error()
	}
	d.SetRows(msg.rows, errStr)
	return tea.Tick(clusterDashboardInterval, func(time.Time) tea.Msg { return clusterDashboardTickMsg{} })
}

// refreshClusterDashboard re-fetches while the dashboard stays open.
func (m *UI) refreshClusterDashboard() tea.Cmd {
	if !m.dialog.ContainsDialog(dialog.ClusterDashboardID) {
		return nil
	}
	return m.fetchClusterDashboard()
}

// moveDialogMsg carries dashboard rows for a move dialog about to open.
type moveDialogMsg struct {
	target dialog.MoveTarget
	rows   []cluster.NodeDashboard
	err    error
}

// moveResultMsg reports a finished move attempt.
type moveResultMsg struct {
	action dialog.ActionMoveSession
	res    proto.MoveSessionResponse
	err    error
}

// openMoveSessionDialog fetches node health and opens the move dialog for
// target.
func (m *UI) openMoveSessionDialog(target dialog.MoveTarget) tea.Cmd {
	ws := m.com.Workspace
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		rows, err := ws.ClusterDashboard(ctx)
		return moveDialogMsg{target: target, rows: rows, err: err}
	}
}

// currentMoveTarget describes the session open in the main pane.
func (m *UI) currentMoveTarget() (dialog.MoveTarget, bool) {
	if m.session == nil {
		return dialog.MoveTarget{}, false
	}
	return dialog.MoveTarget{SessionID: m.session.ID, Title: m.session.Title, OwnerNode: m.com.Workspace.NodeID()}, true
}

func (m *UI) handleMoveDialogData(msg moveDialogMsg) tea.Cmd {
	if msg.err != nil {
		return util.ReportError(fmt.Errorf("cluster unavailable: %w", msg.err))
	}
	selfID := ""
	for _, r := range msg.rows {
		if r.Self {
			selfID = r.Member.NodeID
		}
	}
	m.homeNodeID = selfID
	m.dialog.CloseDialog(dialog.MoveSessionID)
	m.dialog.OpenDialog(dialog.NewMoveSession(m.com, msg.target, msg.rows, selfID))
	return nil
}

// runMoveSession performs the move off the Update goroutine.
func (m *UI) runMoveSession(a dialog.ActionMoveSession) tea.Cmd {
	ws := m.com.Workspace
	return tea.Batch(
		m.startTransferPoll(a.Target.Root),
		util.ReportInfo(fmt.Sprintf("Moving %q to %s…", a.Target.Title, a.Node.Member.Name)),
		func() tea.Msg {
			res, err := ws.MoveSession(context.Background(), a.Target.Root, a.Target.SessionID, a.Node.Member.NodeID, a.OnConflict, true)
			return moveResultMsg{action: a, res: res, err: err}
		},
	)
}

func (m *UI) handleMoveResult(msg moveResultMsg) tea.Cmd {
	m.endTransferPoll()
	d, open := m.dialog.Dialog(dialog.MoveSessionID).(*dialog.MoveSession)
	if msg.err != nil {
		errStr := msg.err.Error()
		if open && strings.Contains(errStr, "already has different state") && msg.action.OnConflict == "" {
			d.ShowConflict(msg.action.Node, errStr)
			return nil
		}
		if open {
			d.ShowError(errStr)
			return nil
		}
		return util.ReportError(msg.err)
	}
	m.dialog.CloseDialog(dialog.MoveSessionID)
	note := fmt.Sprintf("Moved to %s.", msg.res.TargetNodeName)
	if msg.res.BackupPath != "" {
		note += " Their previous files are in " + msg.res.BackupPath + "."
	}
	cmds := []tea.Cmd{util.ReportInfo(note), m.loadWorkspaceOverviews()}
	// Follow the session the user was looking at to its new home so the
	// conversation simply continues there.
	if m.session != nil && m.session.ID == msg.res.SessionID {
		ref := msg.res.TargetNode
		if ref == m.homeNodeID {
			ref = workspace.HomeNodeRef
		}
		cmds = append(cmds, m.switchWorkspaceAndLoad(workspace.QualifyNodeRoot(ref, msg.res.Root), msg.res.SessionID))
	}
	return tea.Batch(cmds...)
}

type (
	migratePlanMsg struct {
		plan proto.MigrationPlan
		all  bool
		err  error
	}
	migrateResultMsg struct {
		res proto.MigrationResult
		err error
	}
)

// planMigration computes a plan off the Update goroutine.
func (m *UI) planMigration(strategy string, all bool) tea.Cmd {
	ws := m.com.Workspace
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		plan, err := ws.PlanMigration(ctx, proto.MigrateRequest{Strategy: strategy, AllSessions: all})
		return migratePlanMsg{plan: plan, all: all, err: err}
	}
}

func (m *UI) handleMigratePlan(msg migratePlanMsg) tea.Cmd {
	if msg.err != nil {
		return util.ReportError(msg.err)
	}
	if d, ok := m.dialog.Dialog(dialog.MigrateID).(*dialog.Migrate); ok {
		d.SetPlan(msg.plan, msg.all)
		return nil
	}
	m.dialog.OpenDialog(dialog.NewMigrate(m.com, msg.plan, msg.all))
	return nil
}

func (m *UI) executeMigration(plan proto.MigrationPlan) tea.Cmd {
	ws := m.com.Workspace
	return tea.Batch(m.startTransferPoll(""), func() tea.Msg {
		res, err := ws.ExecuteMigration(context.Background(), plan, true)
		return migrateResultMsg{res: res, err: err}
	})
}

func (m *UI) handleMigrateResult(msg migrateResultMsg) tea.Cmd {
	m.endTransferPoll()
	if d, ok := m.dialog.Dialog(dialog.MigrateID).(*dialog.Migrate); ok {
		d.SetResult(msg.res, msg.err)
	} else if msg.err != nil {
		return util.ReportError(msg.err)
	}
	return m.loadWorkspaceOverviews()
}

func moveDialogID() string { return dialog.MoveSessionID }
