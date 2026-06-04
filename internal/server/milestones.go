package server

import "net/http"

func (c *controllerV1) handleGetWorkspaceMilestones(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sid := r.PathValue("sid")
	milestones, err := c.backend.ListMilestones(r.Context(), id, sid)
	if err != nil {
		c.handleError(w, r, err)
		return
	}
	jsonEncode(w, milestones)
}
