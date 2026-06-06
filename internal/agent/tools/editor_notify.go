package tools

import (
	"context"
	"log/slog"

	"github.com/taigrr/crush/internal/editor"
)

// notifyEditor fires the editor.Bridge hooks after Crush successfully
// writes a file. The bridge is resolved from ctx (WithEditorBridge), so
// the notification targets the editor of the client that initiated the
// current turn. It is best-effort: any error is logged at debug level
// and never propagated to the agent. Safe to call when no bridge is
// attached (resolves to editor.Noop).
func notifyEditor(ctx context.Context, path, oldContent, newContent string) {
	bridge := EditorBridgeFromContext(ctx)
	if !bridge.Available() {
		return
	}

	startLine, endLine := editor.EditedRange(oldContent, newContent)
	if endLine > startLine {
		if err := bridge.FlashEdit(ctx, path, startLine, endLine); err != nil {
			slog.Debug("Editor flash edit failed", "path", path, "error", err)
		}
	}

	if err := bridge.NotifyFileChanged(ctx, path); err != nil {
		slog.Debug("Editor file-changed notify failed", "path", path, "error", err)
	}
}
