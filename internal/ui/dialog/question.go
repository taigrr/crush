package dialog

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/taigrr/crush/internal/question"
	"github.com/taigrr/crush/internal/ui/common"
)

// QuestionID is the identifier for the question dialog.
const QuestionID = "question"

// questionDialogWidth is the fixed width of the question dialog.
const questionDialogWidth = 64

// yesNoOptions are the display labels shown for [question.KindYesNo].
// The canonical answers sent back over the wire are always lowercase
// "yes"/"no" (yesNoCanonical), independent of display casing.
var (
	yesNoOptions   = []string{"Yes", "No"}
	yesNoCanonical = []string{"yes", "no"}
)

// Question is a dialog that presents a single structured question
// asked by the agent (via the question tool) and collects the user's
// answer: a single choice, zero or more choices, free-form text, or a
// yes/no decision.
type Question struct {
	com *common.Common
	req question.Request

	// options holds the display labels for choice kinds. For
	// KindYesNo this is yesNoOptions rather than req.Options (which is
	// unused for that kind).
	options []string
	cursor  int
	// checked tracks selected indices for KindMultipleChoice.
	checked map[int]bool

	input textinput.Model // used for KindFreeText only

	// hint is a transient validation message shown when the user tries
	// to confirm an empty answer (empty free-text or zero-selection
	// multiple choice). Cleared on the next meaningful input.
	hint string

	help   help.Model
	keyMap questionKeyMap
}

type questionKeyMap struct {
	Up      key.Binding
	Down    key.Binding
	Toggle  key.Binding
	Confirm key.Binding
	Close   key.Binding
}

func defaultQuestionKeyMap() questionKeyMap {
	return questionKeyMap{
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("↑", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("↓", "down"),
		),
		Toggle: key.NewBinding(
			key.WithKeys("space"),
			key.WithHelp("space", "toggle"),
		),
		Confirm: key.NewBinding(
			key.WithKeys("enter", "ctrl+y"),
			key.WithHelp("enter", "confirm"),
		),
		Close: CloseKey,
	}
}

var _ Dialog = (*Question)(nil)

// NewQuestion creates a new question dialog for req.
func NewQuestion(com *common.Common, req question.Request) *Question {
	q := &Question{
		com:     com,
		req:     req,
		keyMap:  defaultQuestionKeyMap(),
		checked: make(map[int]bool),
	}

	switch req.Kind {
	case question.KindYesNo:
		q.options = yesNoOptions
	case question.KindSingleChoice, question.KindMultipleChoice:
		q.options = req.Options
	case question.KindFreeText:
		q.input = textinput.New()
		q.input.SetVirtualCursor(false)
		q.input.Placeholder = "Type your answer..."
		q.input.SetStyles(com.Styles.TextInput)
		q.input.Focus()
		q.input.SetWidth(questionDialogWidth - com.Styles.Dialog.View.GetHorizontalFrameSize() - 2)
	}

	q.help = help.New()
	q.help.Styles = com.Styles.DialogHelpStyles()
	return q
}

// ID implements [Dialog].
func (*Question) ID() string { return QuestionID }

// ToolCallID returns the tool call ID associated with this dialog's
// question request.
func (q *Question) ToolCallID() string { return q.req.ToolCallID }

// SessionID returns the session ID that this dialog's question
// request belongs to.
func (q *Question) SessionID() string { return q.req.SessionID }

// HandleMsg implements [Dialog].
func (q *Question) HandleMsg(msg tea.Msg) Action {
	keyMsg, isKey := msg.(tea.KeyPressMsg)
	if !isKey {
		return nil
	}

	if key.Matches(keyMsg, q.keyMap.Close) {
		return q.respond(question.Answer{Cancelled: true})
	}

	if q.req.Kind == question.KindFreeText {
		if key.Matches(keyMsg, q.keyMap.Confirm) {
			// Block confirming an empty answer so an accidental empty
			// submit can't masquerade as a real answer. The user must
			// type something or explicitly cancel (Escape).
			v := q.input.Value()
			if v == "" {
				q.hint = "Type an answer, or press esc to decline."
				return nil
			}
			return q.respond(question.Answer{Selected: []string{v}})
		}
		var cmd tea.Cmd
		q.input, cmd = q.input.Update(msg)
		q.hint = ""
		if cmd != nil {
			return ActionCmd{cmd}
		}
		return nil
	}

	switch {
	case key.Matches(keyMsg, q.keyMap.Down):
		q.cursor = (q.cursor + 1) % max(1, len(q.options))
	case key.Matches(keyMsg, q.keyMap.Up):
		q.cursor = (q.cursor - 1 + len(q.options)) % max(1, len(q.options))
	case q.req.Kind == question.KindMultipleChoice && key.Matches(keyMsg, q.keyMap.Toggle):
		q.checked[q.cursor] = !q.checked[q.cursor]
		q.hint = ""
	case key.Matches(keyMsg, q.keyMap.Confirm):
		return q.confirmChoice()
	}
	return nil
}

// confirmChoice builds the answer for the current choice-kind
// selection (single choice, yes/no, or multiple choice).
func (q *Question) confirmChoice() Action {
	switch q.req.Kind {
	case question.KindYesNo:
		return q.respond(question.Answer{Selected: []string{yesNoCanonical[q.cursor]}})
	case question.KindMultipleChoice:
		var selected []string
		for i, opt := range q.options {
			if q.checked[i] {
				selected = append(selected, opt)
			}
		}
		// Block confirming with nothing selected so an empty submit
		// can't masquerade as a real answer. The user must select at
		// least one option or explicitly cancel (Escape).
		if len(selected) == 0 {
			q.hint = "Select at least one option (space), or press esc to decline."
			return nil
		}
		return q.respond(question.Answer{Selected: selected})
	default: // KindSingleChoice
		if len(q.options) == 0 {
			return q.respond(question.Answer{})
		}
		return q.respond(question.Answer{Selected: []string{q.options[q.cursor]}})
	}
}

func (q *Question) respond(ans question.Answer) Action {
	ans.ID = q.req.ID
	ans.SessionID = q.req.SessionID
	ans.ToolCallID = q.req.ToolCallID
	return ActionQuestionResponse{Request: q.req, Answer: ans}
}

// Draw implements [Dialog].
func (q *Question) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := q.com.Styles
	dialogStyle := t.Dialog.View.Width(questionDialogWidth)

	var body string
	switch q.req.Kind {
	case question.KindFreeText:
		body = t.Dialog.InputPrompt.Render(q.input.View())
	default:
		body = q.renderOptions()
	}

	lines := []string{
		t.Dialog.TitleText.Render(q.title()),
		"",
		t.Dialog.PrimaryText.Render(q.req.Prompt),
		"",
		body,
	}
	if q.hint != "" {
		lines = append(lines, "", t.Dialog.SecondaryText.Render(q.hint))
	}
	lines = append(lines, "", t.Dialog.HelpView.Render(q.help.View(q)))
	content := strings.Join(lines, "\n")

	view := dialogStyle.Render(content)

	if q.req.Kind == question.KindFreeText {
		cur := q.input.Cursor()
		DrawCenterCursor(scr, area, view, cur)
		return cur
	}
	DrawCenter(scr, area, view)
	return nil
}

func (q *Question) title() string {
	switch q.req.Kind {
	case question.KindYesNo:
		return "Question (yes/no)"
	case question.KindMultipleChoice:
		return "Question (select any)"
	case question.KindFreeText:
		return "Question"
	default:
		return "Question (select one)"
	}
}

// renderOptions renders the option list for choice-kind questions,
// highlighting the row under the cursor and, for multiple choice,
// prefixing a checkbox marker.
func (q *Question) renderOptions() string {
	t := q.com.Styles
	lines := make([]string, len(q.options))
	for i, opt := range q.options {
		label := opt
		if q.req.Kind == question.KindMultipleChoice {
			marker := "[ ]"
			if q.checked[i] {
				marker = "[x]"
			}
			label = fmt.Sprintf("%s %s", marker, opt)
		}
		style := t.Dialog.NormalItem
		if i == q.cursor {
			style = t.Dialog.SelectedItem
		}
		lines[i] = style.Render(label)
	}
	return strings.Join(lines, "\n")
}

// ShortHelp implements [help.KeyMap].
func (q *Question) ShortHelp() []key.Binding {
	if q.req.Kind == question.KindFreeText {
		return []key.Binding{q.keyMap.Confirm, q.keyMap.Close}
	}
	return []key.Binding{q.keyMap.Up, q.keyMap.Down, q.keyMap.Confirm}
}

// FullHelp implements [help.KeyMap].
func (q *Question) FullHelp() [][]key.Binding {
	if q.req.Kind == question.KindFreeText {
		return [][]key.Binding{{q.keyMap.Confirm, q.keyMap.Close}}
	}
	if q.req.Kind == question.KindMultipleChoice {
		return [][]key.Binding{{q.keyMap.Up, q.keyMap.Down, q.keyMap.Toggle, q.keyMap.Confirm}, {q.keyMap.Close}}
	}
	return [][]key.Binding{{q.keyMap.Up, q.keyMap.Down, q.keyMap.Confirm}, {q.keyMap.Close}}
}
