package model

import (
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/taigrr/crush/internal/agent/notify"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/question"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/termstatus"
)

const programStatusWorking = "Working"

// programStatusParent is the record every session record lives under.
const programStatusParent = "s"

// programStatusRecord is one session's OSC 7501 record. Every session,
// including the open one, is a child of "s": the root record is never
// used, because replacing it clears every record on the terminal.
type programStatusRecord struct {
	id    string
	state termstatus.State
	kind  termstatus.Kind
	title string
	msg   string
}

func (r programStatusRecord) report() termstatus.Report {
	return termstatus.Report{
		State:   r.state,
		ID:      r.id,
		Kind:    r.kind,
		App:     termstatus.AppCrush,
		Title:   r.title,
		Message: r.msg,
	}
}

func (r programStatusRecord) same(o programStatusRecord) bool {
	return r == o
}

// programStatusCmd writes one record per known session. Records that
// disappeared are cleared. It returns nil when nothing changed, so a
// steady frame writes nothing.
func (m *UI) programStatusCmd() tea.Cmd {
	if m.quitting {
		return nil
	}
	children := m.programStatusRecords()
	if m.programStatusInit && sameRecords(children, m.programStatusKids) {
		return nil
	}
	var b strings.Builder
	if m.programStatusInit {
		for id, prev := range m.programStatusKids {
			if _, ok := children[id]; !ok {
				b.WriteString(termstatus.Report{State: termstatus.StateClear, ID: prev.id}.Sequence())
			}
		}
	}
	for _, rec := range children {
		if prev, ok := m.programStatusKids[rec.id]; ok && prev.same(rec) {
			continue
		}
		b.WriteString(rec.report().Sequence())
	}
	m.programStatusKids = children
	m.programStatusInit = true
	if b.Len() == 0 {
		return nil
	}
	return tea.Raw(b.String())
}

// programStatusClearCmd removes every record this program wrote. It is
// used only when the user quits: a SIGHUP or SIGTERM exit leaves records
// for the terminal to drop on its own. Clearing the parent "s" clears
// every session record beneath it.
func (m *UI) programStatusClearCmd() tea.Cmd {
	if !m.programStatusInit || len(m.programStatusKids) == 0 {
		return nil
	}
	m.programStatusKids = nil
	return tea.Raw(termstatus.Report{State: termstatus.StateClear, ID: programStatusParent}.Sequence())
}

func sameRecords(a, b map[string]programStatusRecord) bool {
	if len(a) != len(b) {
		return false
	}
	for id, rec := range a {
		if !b[id].same(rec) {
			return false
		}
	}
	return true
}

// programStatusRecords derives one record per session. The open session
// is taken from live UI state, which is newer than the overview snapshot.
func (m *UI) programStatusRecords() map[string]programStatusRecord {
	open := ""
	if m.hasSession() {
		open = m.session.ID
	}
	children := make(map[string]programStatusRecord, len(m.programStatusKids)+1)
	if m.leftSidebar != nil {
		for _, ws := range m.leftSidebar.Overviews() {
			for _, sess := range ws.Sessions {
				if sess.ID == "" || sess.ID == open {
					continue
				}
				if sess.IsBusy {
					delete(m.sessionOutcome, sess.ID)
				}
				rec := sessionRecord(sess, m.pendingPermissions[sess.ID], m.pendingQuestions[sess.ID], m.attentionPending[sess.ID], m.sessionOutcome[sess.ID])
				children[rec.id] = rec
			}
		}
	}
	if open == "" {
		return children
	}
	if m.isSessionBusy() {
		delete(m.sessionOutcome, open)
	}
	rec := openRecord(m, m.pendingPermissions[open], m.pendingQuestions[open])
	children[rec.id] = rec
	return children
}

// openRecord reports the session the user is looking at. It is called
// only when one is open.
func openRecord(m *UI, perm *permission.PermissionRequest, q *question.Request) programStatusRecord {
	id := m.session.ID
	rec := programStatusRecord{id: recordID(id), title: programStatusTitle(m.session.Title), state: termstatus.StateIdle, msg: "Ready"}
	switch {
	case perm != nil:
		rec.state, rec.kind, rec.msg = termstatus.StateBlocked, termstatus.KindPermission, firstNonEmpty(perm.Description, "Permission required")
	case q != nil:
		rec.state, rec.kind, rec.msg = termstatus.StateBlocked, termstatus.KindQuestion, firstNonEmpty(q.Prompt, "The agent has a question")
	case m.isSessionBusy():
		rec.state, rec.msg = termstatus.StateWorking, programStatusWorking
	default:
		if out, ok := m.sessionOutcome[id]; ok {
			rec.state, rec.msg = out.state, out.msg
		}
	}
	return rec
}

func firstNonEmpty(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

// programStatusTitle is the label for a record. "New Session" is the
// placeholder written at creation, before the generated title exists,
// so it is omitted until that title arrives.
func programStatusTitle(title string) string {
	if title == "" || title == "New Session" {
		return ""
	}
	return title
}

func sessionRecord(sess proto.SessionOverview, perm *permission.PermissionRequest, q *question.Request, attention bool, out sessionOutcome) programStatusRecord {
	rec := programStatusRecord{id: recordID(sess.ID), title: programStatusTitle(sess.Title), state: termstatus.StateIdle, msg: "Idle"}
	switch {
	case perm != nil:
		rec.state, rec.kind, rec.msg = termstatus.StateBlocked, termstatus.KindPermission, firstNonEmpty(perm.Description, "Permission required")
	case q != nil:
		rec.state, rec.kind, rec.msg = termstatus.StateBlocked, termstatus.KindQuestion, firstNonEmpty(q.Prompt, "The agent has a question")
	case attention:
		// The kind is not known here: the signal covers both prompts.
		rec.state = termstatus.StateBlocked
		rec.msg = "Needs your input"
	case sess.IsBusy:
		rec.state = termstatus.StateWorking
		rec.msg = programStatusWorking
	case out.state != "":
		rec.state = out.state
		rec.msg = out.msg
	case sess.Unread:
		rec.state = termstatus.StateDone
		rec.msg = "Ready for review"
	}
	if rec.msg == "" {
		rec.msg = string(rec.state)
	}
	return rec
}

// recordID is the session hash under a fixed parent. The root record is
// never written: replacing it clears every record on the terminal. An
// empty id has no record.
func recordID(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	if id, ok := recordIDs.Load(sessionID); ok {
		return id.(string)
	}
	id := programStatusParent + "/" + session.HashID(sessionID)
	recordIDs.Store(sessionID, id)
	return id
}

// recordIDs memoizes [recordID]. Records are rebuilt on every Update,
// and hashing every session each time dominated the cost.
var recordIDs sync.Map

// sessionOutcome is the last terminal result of a session, kept so a
// finished or failed turn stays reported until the next one starts.
type sessionOutcome struct {
	state termstatus.State
	msg   string
}

func (m *UI) noteSessionOutcome(id string, state termstatus.State, msg string) {
	if id == "" {
		return
	}
	if m.sessionOutcome == nil {
		m.sessionOutcome = map[string]sessionOutcome{}
	}
	if msg == "" {
		msg = string(state)
	}
	m.sessionOutcome[id] = sessionOutcome{state: state, msg: msg}
}

func (m *UI) noteRunComplete(rc notify.RunComplete) {
	switch {
	case rc.Cancelled:
		m.noteSessionOutcome(rc.SessionID, termstatus.StateIdle, "Cancelled")
	case rc.Error != "":
		m.noteSessionOutcome(rc.SessionID, termstatus.StateError, rc.Error)
	default:
		msg := "Finished"
		if t := strings.TrimSpace(rc.Text); t != "" {
			if i := strings.IndexAny(t, "\r\n"); i >= 0 {
				t = t[:i]
			}
			msg = t
		}
		m.noteSessionOutcome(rc.SessionID, termstatus.StateDone, msg)
	}
}
