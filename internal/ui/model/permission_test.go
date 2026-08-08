package model

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/dialog"
)

// newTestUIForPermissions builds a UI with a chat, dialog overlay, and
// common context sufficient to exercise the permission dialog paths. It
// uses a stub workspace so m.com.Config() is reachable (it returns nil,
// which openPermissionsDialog tolerates).
func newTestUIForPermissions() *UI {
	u := newTestUI()
	u.com = common.DefaultCommon(&sendWorkspace{})
	u.dialog = dialog.NewOverlay()
	return u
}

func TestHandlePermissionNotification_RemoteGrantClosesDialog(t *testing.T) {
	t.Parallel()

	u := newTestUIForPermissions()
	perm := permission.PermissionRequest{
		ID:         "perm-1",
		ToolCallID: "tool-call-X",
		ToolName:   "bash",
	}
	u.dialog.OpenDialogWithGrace(dialog.NewPermissions(u.com, perm))
	require.True(t, u.dialog.ContainsDialog(dialog.PermissionsID))

	u.handlePermissionNotification(permission.PermissionNotification{
		ToolCallID: "tool-call-X",
		Granted:    true,
	})

	require.False(t, u.dialog.ContainsDialog(dialog.PermissionsID),
		"granted notification should close matching permissions dialog")
}

func TestHandlePermissionNotification_RemoteDenyClosesDialog(t *testing.T) {
	t.Parallel()

	u := newTestUIForPermissions()
	perm := permission.PermissionRequest{
		ID:         "perm-2",
		ToolCallID: "tool-call-Y",
	}
	u.dialog.OpenDialogWithGrace(dialog.NewPermissions(u.com, perm))

	u.handlePermissionNotification(permission.PermissionNotification{
		ToolCallID: "tool-call-Y",
		Denied:     true,
	})

	require.False(t, u.dialog.ContainsDialog(dialog.PermissionsID),
		"denied notification should close matching permissions dialog")
}

func TestHandlePermissionNotification_InitialPendingDoesNotClose(t *testing.T) {
	t.Parallel()

	u := newTestUIForPermissions()
	perm := permission.PermissionRequest{
		ID:         "perm-3",
		ToolCallID: "tool-call-Z",
	}
	u.dialog.OpenDialogWithGrace(dialog.NewPermissions(u.com, perm))

	// The initial notification published by permission.Request is
	// neither granted nor denied; it must not dismiss the dialog.
	u.handlePermissionNotification(permission.PermissionNotification{
		ToolCallID: "tool-call-Z",
	})

	require.True(t, u.dialog.ContainsDialog(dialog.PermissionsID),
		"initial pending notification must not close the dialog")
}

func TestHandlePermissionNotification_DifferentToolCallIDDoesNotClose(t *testing.T) {
	t.Parallel()

	u := newTestUIForPermissions()
	perm := permission.PermissionRequest{
		ID:         "perm-4",
		ToolCallID: "tool-call-A",
	}
	u.dialog.OpenDialogWithGrace(dialog.NewPermissions(u.com, perm))

	u.handlePermissionNotification(permission.PermissionNotification{
		ToolCallID: "tool-call-B",
		Granted:    true,
	})

	require.True(t, u.dialog.ContainsDialog(dialog.PermissionsID),
		"notification for unrelated tool call must not close the dialog")
}

func TestSyncPermissionDialog_ClosesForeignSessionDialog(t *testing.T) {
	t.Parallel()

	u := newTestUIForPermissions()
	u.session = &session.Session{ID: "session-B"}

	// A dialog for session A is open while the user views session B.
	perm := permission.PermissionRequest{
		ID:         "perm-1",
		SessionID:  "session-A",
		ToolCallID: "tool-A",
	}
	u.dialog.OpenDialogWithGrace(dialog.NewPermissions(u.com, perm))
	require.True(t, u.dialog.ContainsDialog(dialog.PermissionsID))

	u.syncPermissionDialogForSession()

	require.False(t, u.dialog.ContainsDialog(dialog.PermissionsID),
		"a dialog belonging to another session must be closed on switch")
}

func TestSyncPermissionDialog_ResurfacesPendingForActiveSession(t *testing.T) {
	t.Parallel()

	u := newTestUIForPermissions()
	u.session = &session.Session{ID: "session-A"}

	// A request for session A arrived while the user was elsewhere; it is
	// cached but no dialog is open.
	u.pendingPermissions["session-A"] = &permission.PermissionRequest{
		ID:         "perm-2",
		SessionID:  "session-A",
		ToolCallID: "tool-A",
	}

	cmd := u.syncPermissionDialogForSession()
	require.Nil(t, cmd) // openPermissionsDialog returns a nil cmd
	require.True(t, u.dialog.ContainsDialog(dialog.PermissionsID),
		"a pending request for the active session must be re-surfaced")
}

func TestSyncPermissionDialog_DoesNotResurfaceForOtherSession(t *testing.T) {
	t.Parallel()

	u := newTestUIForPermissions()
	u.session = &session.Session{ID: "session-B"}
	u.pendingPermissions["session-A"] = &permission.PermissionRequest{
		ID:         "perm-3",
		SessionID:  "session-A",
		ToolCallID: "tool-A",
	}

	u.syncPermissionDialogForSession()

	require.False(t, u.dialog.ContainsDialog(dialog.PermissionsID),
		"a pending request for a different session must not be shown")
}

func TestHandlePermissionNotification_ClearsPendingCache(t *testing.T) {
	t.Parallel()

	u := newTestUIForPermissions()
	u.session = &session.Session{ID: "session-A"}
	u.pendingPermissions["session-A"] = &permission.PermissionRequest{
		ID:         "perm-4",
		SessionID:  "session-A",
		ToolCallID: "tool-A",
	}

	u.handlePermissionNotification(permission.PermissionNotification{
		SessionID:  "session-A",
		ToolCallID: "tool-A",
		Granted:    true,
	})

	require.Nil(t, u.pendingPermissions["session-A"],
		"resolving the request must clear the pending cache so it is not re-surfaced")
}
