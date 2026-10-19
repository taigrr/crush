package headless

import (
	"fmt"
	"strings"

	"github.com/taigrr/crush/internal/proto"
)

// PermissionMode selects how a headless run resolves tool permission
// requests.
type PermissionMode string

const (
	// ModeYOLO approves every request. It is the historical `crush run`
	// behavior and the default.
	ModeYOLO PermissionMode = "yolo"
	// ModeDeny denies every request not pre-approved by the
	// allowed_tools list or a PreToolUse hook; the run stops on the
	// first denial.
	ModeDeny PermissionMode = "deny"
	// ModeDelegate leaves requests (and questions) pending so the caller
	// can decide with `crush session answer`; the run reports
	// [StatusBlocked] while it waits.
	ModeDelegate PermissionMode = "delegate"
)

// PermissionModes lists every accepted mode in display order.
var PermissionModes = []PermissionMode{ModeYOLO, ModeDeny, ModeDelegate}

// ParsePermissionMode validates s. The empty string selects [ModeYOLO].
func ParsePermissionMode(s string) (PermissionMode, error) {
	if s == "" {
		return ModeYOLO, nil
	}
	for _, m := range PermissionModes {
		if strings.EqualFold(s, string(m)) {
			return m, nil
		}
	}
	names := make([]string, len(PermissionModes))
	for i, m := range PermissionModes {
		names[i] = string(m)
	}
	return "", fmt.Errorf("invalid permission mode %q (want one of %s)", s, strings.Join(names, ", "))
}

// Policy returns the per-session server policy implementing the mode.
func (m PermissionMode) Policy() proto.PermissionPolicy {
	switch m {
	case ModeDeny:
		return proto.PermissionPolicyDeny
	case ModeDelegate:
		return proto.PermissionPolicyPrompt
	default:
		return proto.PermissionPolicyAllow
	}
}

// Delegates reports whether pending prompts are handed to the caller.
func (m PermissionMode) Delegates() bool { return m == ModeDelegate }
