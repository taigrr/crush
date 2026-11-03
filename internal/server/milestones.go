package server

import (
	"net/http"

	"github.com/taigrr/crush/internal/milestone"
	"github.com/taigrr/crush/internal/proto"
)

// swag resolves the types in the annotations below through these imports.
var (
	_ milestone.Milestone
	_ proto.Error
)

// handleGetWorkspaceMilestones lists a session's milestones.
//
//	@Summary		List session milestones
//	@Tags			sessions
//	@Produce		json
//	@Param			id	path		string	true	"Workspace ID"
//	@Param			sid	path		string	true	"Session ID"
//	@Success		200	{array}		milestone.Milestone
//	@Failure		404	{object}	proto.Error
//	@x-crush-scope	"sessions:read"
//	@Router			/workspaces/{id}/sessions/{sid}/milestones [get]
func (c *controllerV1) handleGetWorkspaceMilestones(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sid := r.PathValue("sid")
	milestones, err := c.backend.ListMilestones(r.Context(), id, sid)
	c.respond(w, r, milestones, err)
}
