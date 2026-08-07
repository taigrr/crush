// Package question implements the synchronous agent<->UI round trip for
// the question tool: an agent tool blocks on [Service.Ask] until a
// client answers (or cancels) the pending request, mirroring the
// permission package's Request/Grant/Deny machinery.
package question
