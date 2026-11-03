package server

import (
	"errors"
	"net/http"

	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/voice"
)

// handleGetSessionModel returns the model a session runs on.
//
//	@Summary		Get a session's model
//	@Description	The large model is per-session (model_ref); the small model is always the workspace's.
//	@Tags			sessions
//	@Produce		json
//	@Param			id	path		string	true	"Workspace ID"
//	@Param			sid	path		string	true	"Session ID"
//	@Success		200	{object}	proto.SessionModel
//	@Failure		404	{object}	proto.Error
//	@Failure		500	{object}	proto.Error
//	@x-crush-scope	"sessions:read"
//	@Router			/workspaces/{id}/sessions/{sid}/model [get]
func (c *controllerV1) handleGetSessionModel(w http.ResponseWriter, r *http.Request) {
	m, err := c.backend.GetSessionModel(r.Context(), r.PathValue("id"), r.PathValue("sid"))
	c.respond(w, r, m, err)
}

// handleSetSessionModel sets the model a session's turns run on without
// changing the workspace default.
//
//	@Summary		Set a session's model
//	@Description	Sets the session's large model (model_ref or model); an empty body clears it so the session follows the workspace default. model_type "small" is rejected: the small model is workspace-wide.
//	@Tags			sessions
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string						true	"Workspace ID"
//	@Param			sid		path		string						true	"Session ID"
//	@Param			request	body		proto.SessionModelRequest	true	"Session model"
//	@Success		200		{object}	proto.SessionModel
//	@Failure		400		{object}	proto.Error
//	@Failure		404		{object}	proto.Error
//	@Failure		409		{object}	proto.Error
//	@Failure		500		{object}	proto.Error
//	@x-crush-scope	"config:model"
//	@Router			/workspaces/{id}/sessions/{sid}/model [post]
func (c *controllerV1) handleSetSessionModel(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeJSON[proto.SessionModelRequest](c, w, r)
	if !ok {
		return
	}
	m, err := c.backend.SetSessionModel(r.Context(), r.PathValue("id"), r.PathValue("sid"), req)
	c.respond(w, r, m, err)
}

// handlePutSessionModel is handleSetSessionModel for PUT. Devices use POST
// (the bridge routes only GET and POST).
//
//	@Summary		Set a session's model
//	@Tags			sessions
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string						true	"Workspace ID"
//	@Param			sid		path		string						true	"Session ID"
//	@Param			request	body		proto.SessionModelRequest	true	"Session model"
//	@Success		200		{object}	proto.SessionModel
//	@Failure		400		{object}	proto.Error
//	@Failure		404		{object}	proto.Error
//	@Failure		409		{object}	proto.Error
//	@Failure		500		{object}	proto.Error
//	@x-crush-scope	"never"
//	@Router			/workspaces/{id}/sessions/{sid}/model [put]
func (c *controllerV1) handlePutSessionModel(w http.ResponseWriter, r *http.Request) {
	c.handleSetSessionModel(w, r)
}

// handlePostVoiceClientSecret mints a short-lived xAI realtime voice
// secret with this node's Grok credential.
//
//	@Summary		Mint a realtime voice client secret
//	@Description	Returns an ephemeral xAI realtime secret. The node's credential never leaves it. 424 when no xAI credential is configured.
//	@Tags			voice
//	@Accept			json
//	@Produce		json
//	@Param			request	body		proto.VoiceClientSecretRequest	true	"Voice secret request"
//	@Success		200		{object}	proto.VoiceClientSecret
//	@Failure		400		{object}	proto.Error
//	@Failure		424		{object}	proto.Error
//	@Failure		502		{object}	proto.Error
//	@x-crush-scope	"sessions:prompt"
//	@Router			/voice/client-secret [post]
func (c *controllerV1) handlePostVoiceClientSecret(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeJSON[proto.VoiceClientSecretRequest](c, w, r)
	if !ok {
		return
	}
	sec, err := c.backend.VoiceClientSecret(r.Context(), req.SessionID)
	var upstream *voice.UpstreamError
	switch {
	case err == nil:
		w.Header().Set("Cache-Control", "no-store")
		jsonEncode(w, proto.VoiceClientSecret{Value: sec.Value, ExpiresAt: sec.ExpiresAt.Unix()})
	case errors.Is(err, voice.ErrNoCredential):
		jsonError(w, http.StatusFailedDependency, err.Error())
	case errors.As(err, &upstream):
		c.server.logError(r, "Voice secret mint failed", "status", upstream.Status)
		jsonError(w, http.StatusBadGateway, err.Error())
	default:
		c.handleError(w, r, err)
	}
}
