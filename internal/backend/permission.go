package backend

import (
	"context"

	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/proto"
)

// GrantPermission grants, denies, or persistently grants a permission
// request. The returned bool reports whether this call resolved the
// pending request (true) or found it already resolved by a previous
// caller (false). A false return is not an error.
func (b *Backend) GrantPermission(workspaceID string, req proto.PermissionGrant) (bool, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return false, err
	}

	perm := permission.PermissionRequest{
		ID:          req.Permission.ID,
		SessionID:   req.Permission.SessionID,
		ToolCallID:  req.Permission.ToolCallID,
		ToolName:    req.Permission.ToolName,
		Description: req.Permission.Description,
		Action:      req.Permission.Action,
		Params:      req.Permission.Params,
		Path:        req.Permission.Path,
	}

	switch req.Action {
	case proto.PermissionAllow:
		return ws.Permissions.Grant(perm), nil
	case proto.PermissionAllowForSession:
		return ws.Permissions.GrantPersistent(perm), nil
	case proto.PermissionDeny:
		return ws.Permissions.Deny(perm), nil
	default:
		return false, ErrInvalidPermissionAction
	}
}

// SetPermissionsSkip sets whether permission prompts are skipped.
func (b *Backend) SetPermissionsSkip(workspaceID string, skip bool) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}

	ws.Permissions.SetSkipRequests(skip)
	// Keep the config-store override in sync so proto reporting
	// (workspaceToProto's YOLO field) reflects the runtime toggle while
	// the workspace is live, and remember it per-root so the setting
	// survives an idle-teardown/recreate cycle (see Backend.yoloByRoot).
	if ws.Cfg != nil {
		ws.Cfg.Overrides().SkipPermissionRequests = skip
	}
	b.mu.Lock()
	b.yoloByRoot[ws.resolvedPath] = skip
	b.mu.Unlock()
	return nil
}

// GetPermissionsSkip returns whether permission prompts are skipped.
func (b *Backend) GetPermissionsSkip(workspaceID string) (bool, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return false, err
	}

	return ws.Permissions.SkipRequests(), nil
}

// SetPermissionsSysadmin toggles ephemeral sysadmin mode for a workspace.
func (b *Backend) SetPermissionsSysadmin(workspaceID string, enabled bool) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}

	ws.Permissions.SetSysadminMode(enabled)
	return nil
}

// GetPermissionsSysadmin returns whether ephemeral sysadmin mode is on.
func (b *Backend) GetPermissionsSysadmin(workspaceID string) (bool, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return false, err
	}

	return ws.Permissions.SysadminMode(), nil
}

// maxSessionDepth bounds the parent walk in [Backend.PendingPrompts] so
// a corrupt parent cycle cannot loop forever.
const maxSessionDepth = 16

// PendingPrompts returns the permission requests and questions that
// sessionID, or any sub-agent session beneath it, is blocked on.
func (b *Backend) PendingPrompts(ctx context.Context, workspaceID, sessionID string) (proto.PendingPrompts, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return proto.PendingPrompts{}, err
	}

	out := proto.PendingPrompts{
		Permissions: []proto.PermissionRequest{},
		Questions:   []proto.QuestionRequest{},
	}
	if ws.App == nil {
		return out, nil
	}
	owned := ownershipResolver(ctx, ws, sessionID)

	if ws.Permissions != nil {
		for _, req := range ws.Permissions.PendingRequests("") {
			if !owned(req.SessionID) {
				continue
			}
			out.Permissions = append(out.Permissions, proto.PermissionRequest{
				ID:          req.ID,
				SessionID:   req.SessionID,
				ToolCallID:  req.ToolCallID,
				ToolName:    req.ToolName,
				Description: req.Description,
				Action:      req.Action,
				Params:      req.Params,
				Path:        req.Path,
			})
		}
	}
	if ws.Questions != nil {
		for _, q := range ws.Questions.PendingRequests("") {
			if !owned(q.SessionID) {
				continue
			}
			out.Questions = append(out.Questions, proto.QuestionRequest{
				ID:         q.ID,
				SessionID:  q.SessionID,
				ToolCallID: q.ToolCallID,
				Kind:       proto.QuestionKind(q.Kind),
				Prompt:     q.Prompt,
				Options:    q.Options,
			})
		}
	}
	return out, nil
}

// ownershipResolver returns a predicate reporting whether a session is
// root or one of its descendants. Lookups are memoized per call.
func ownershipResolver(ctx context.Context, ws *Workspace, root string) func(string) bool {
	cache := map[string]bool{root: true}
	return func(id string) bool {
		var chain []string
		owned := false
		current := id
		for range maxSessionDepth {
			if v, ok := cache[current]; ok {
				owned = v
				break
			}
			chain = append(chain, current)
			if ws.Sessions == nil {
				break
			}
			sess, err := ws.Sessions.Get(ctx, current)
			if err != nil || sess.ParentSessionID == "" {
				break
			}
			current = sess.ParentSessionID
		}
		for _, c := range chain {
			cache[c] = owned
		}
		return owned
	}
}
