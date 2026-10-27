package dialog

import (
	"fmt"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/taigrr/crush/internal/ui/common"
)

// ClusterJoinID is the identifier for the cluster join approval dialog.
const ClusterJoinID = "cluster_join"

// ClusterJoin asks the user to approve or deny a machine that wants to
// join this node's cluster. It defaults to "Deny" so a stray enter never
// admits a node.
type ClusterJoin struct {
	com         *common.Common
	requestID   string
	recordedOn  string
	nodeName    string
	selectedYes bool
	keyMap      struct {
		LeftRight, EnterSpace, Yes, No, Tab, Close key.Binding
	}
}

var _ Dialog = (*ClusterJoin)(nil)

// NewClusterJoin creates the approval dialog for one join request.
// recordedOn is the cluster node holding the request ("" = the attached
// server).
func NewClusterJoin(com *common.Common, requestID, nodeName, recordedOn string) *ClusterJoin {
	d := &ClusterJoin{com: com, requestID: requestID, nodeName: nodeName, recordedOn: recordedOn}
	d.keyMap.LeftRight = key.NewBinding(key.WithKeys("left", "right"), key.WithHelp("←/→", "switch options"))
	d.keyMap.EnterSpace = key.NewBinding(key.WithKeys("enter", " "), key.WithHelp("enter/space", "confirm"))
	d.keyMap.Yes = key.NewBinding(key.WithKeys("y", "Y"), key.WithHelp("y", "approve"))
	d.keyMap.No = key.NewBinding(key.WithKeys("n", "N"), key.WithHelp("n", "deny"))
	d.keyMap.Tab = key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "switch options"))
	d.keyMap.Close = CloseKey
	return d
}

// RequestID returns the join request this dialog decides.
func (d *ClusterJoin) RequestID() string { return d.requestID }

// ID implements [Model].
func (*ClusterJoin) ID() string { return ClusterJoinID }

// HandleMsg implements [Model].
func (d *ClusterJoin) HandleMsg(msg tea.Msg) Action {
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch {
	case key.Matches(kp, d.keyMap.Yes):
		return ActionClusterJoinDecision{RequestID: d.requestID, NodeName: d.nodeName, RecordedOn: d.recordedOn, Approve: true}
	case key.Matches(kp, d.keyMap.No):
		return ActionClusterJoinDecision{RequestID: d.requestID, NodeName: d.nodeName}
	case key.Matches(kp, d.keyMap.Close):
		// Dismissing leaves the request pending; it can still be decided
		// later with `crush cluster approve`.
		return ActionClose{}
	case key.Matches(kp, d.keyMap.LeftRight, d.keyMap.Tab):
		d.selectedYes = !d.selectedYes
	case key.Matches(kp, d.keyMap.EnterSpace):
		return ActionClusterJoinDecision{RequestID: d.requestID, NodeName: d.nodeName, RecordedOn: d.recordedOn, Approve: d.selectedYes}
	}
	return nil
}

// Draw implements [Dialog].
func (d *ClusterJoin) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	question := fmt.Sprintf("%q wants to join your Crush cluster.", d.nodeName)
	detail := fmt.Sprintf("Request %s. Approve only machines you control.", d.requestID)
	buttons := common.ButtonGroup(d.com.Styles, []common.ButtonOpts{
		{Text: "Approve", Selected: d.selectedYes, Padding: 3},
		{Text: "Deny", Selected: !d.selectedYes, Padding: 3},
	}, " ")
	content := d.com.Styles.Dialog.Quit.Content.Render(
		lipgloss.JoinVertical(lipgloss.Center, question, detail, "", buttons),
	)
	DrawCenter(scr, area, d.com.Styles.Dialog.Quit.Frame.Render(content))
	return nil
}

// ShortHelp implements [help.KeyMap].
func (d *ClusterJoin) ShortHelp() []key.Binding {
	return []key.Binding{d.keyMap.Yes, d.keyMap.No, d.keyMap.Close}
}

// FullHelp implements [help.KeyMap].
func (d *ClusterJoin) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{d.keyMap.LeftRight, d.keyMap.EnterSpace, d.keyMap.Yes, d.keyMap.No},
		{d.keyMap.Tab, d.keyMap.Close},
	}
}
