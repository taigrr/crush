package server

import (
	"errors"
	"net/http"

	"github.com/taigrr/crush/internal/backend"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/transfer"
)

func (s *Server) installTransferRoutes(mux *routeMux) {
	mux.HandleFunc("POST /v1/sessions/move", s.handleMoveSession)
	mux.HandleFunc("POST /v1/transfer/preflight", s.handleTransferPreflight)
	mux.HandleFunc("POST /v1/transfer/import", s.handleTransferImport)
	mux.HandleFunc("GET /v1/transfers", s.handleGetTransfers)
	mux.HandleFunc("GET /v1/transfers/incoming", s.handleGetIncomingTransfers)
	mux.HandleFunc("POST /v1/migrate/plan", s.handleMigratePlan)
	mux.HandleFunc("POST /v1/migrate/execute", s.handleMigrateExecute)
}

// handleGetTransfers handles "list outgoing transfers".
//
//	@Summary		List outgoing transfers
//	@Tags			transfer
//	@Produce		json
//	@x-crush-scope	"never"
//	@Router			/transfers [get]
func (s *Server) handleGetTransfers(w http.ResponseWriter, _ *http.Request) {
	jsonEncode(w, s.backend.Transfers())
}

// handleGetIncomingTransfers handles "list incoming transfers".
//
//	@Summary		List incoming transfers
//	@Tags			transfer
//	@Produce		json
//	@x-crush-scope	"never"
//	@Router			/transfers/incoming [get]
func (s *Server) handleGetIncomingTransfers(w http.ResponseWriter, _ *http.Request) {
	jsonEncode(w, s.backend.IncomingTransfers())
}

// transferStatus maps transfer errors to HTTP statuses.
func transferStatus(err error) int {
	var ce *transfer.ConflictError
	switch {
	case errors.As(err, &ce), errors.Is(err, transfer.ErrSessionOwned), errors.Is(err, transfer.ErrDestinationBusy):
		return http.StatusConflict
	case errors.Is(err, transfer.ErrSchemaMismatch), errors.Is(err, transfer.ErrFormat):
		return http.StatusPreconditionFailed
	case errors.Is(err, backend.ErrNoCluster):
		return http.StatusNotImplemented
	case errors.Is(err, backend.ErrWorkspaceNotFound):
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

// handleMoveSession moves a session to another node.
//
//	@Summary		Move a session
//	@Tags			transfer
//	@Accept			json
//	@Produce		json
//	@Param			request	body		proto.MoveSessionRequest	true	"Move request"
//	@Success		200		{object}	proto.MoveSessionResponse
//	@Failure		400		{object}	proto.Error
//	@x-crush-scope	"sessions:move"
//	@Router			/sessions/move [post]
func (s *Server) handleMoveSession(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeBody[proto.MoveSessionRequest](w, r)
	if !ok {
		return
	}
	res, err := s.backend.MoveSession(r.Context(), req)
	if err != nil {
		jsonError(w, transferStatus(err), err.Error())
		return
	}
	jsonEncode(w, res)
}

// handleTransferPreflight handles "check a transfer bundle".
//
//	@Summary		Check a transfer bundle
//	@Tags			transfer
//	@Produce		json
//	@x-crush-scope	"never"
//	@Router			/transfer/preflight [post]
func (s *Server) handleTransferPreflight(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeBody[proto.TransferPreflightRequest](w, r)
	if !ok {
		return
	}
	res, err := s.backend.TransferPreflight(r.Context(), req)
	if err != nil {
		jsonError(w, transferStatus(err), err.Error())
		return
	}
	jsonEncode(w, res)
}

// handleTransferImport handles "import a transfer bundle".
//
//	@Summary		Import a transfer bundle
//	@Tags			transfer
//	@Produce		json
//	@x-crush-scope	"never"
//	@Router			/transfer/import [post]
func (s *Server) handleTransferImport(w http.ResponseWriter, r *http.Request) {
	plan := transfer.Plan(r.URL.Query().Get("plan"))
	switch plan {
	case transfer.PlanFresh, transfer.PlanOverlay, transfer.PlanBackup, transfer.PlanDBOnly:
	default:
		jsonError(w, http.StatusBadRequest, "invalid plan")
		return
	}
	res, err := s.backend.ImportTransfer(r.Context(), r.Body, plan, r.URL.Query().Get("resume") != "")
	if err != nil {
		jsonError(w, transferStatus(err), err.Error())
		return
	}
	jsonEncode(w, res)
}

// handleMigratePlan handles "plan a migration".
//
//	@Summary		Plan a migration
//	@Tags			transfer
//	@Produce		json
//	@x-crush-scope	"never"
//	@Router			/migrate/plan [post]
func (s *Server) handleMigratePlan(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeBody[proto.MigrateRequest](w, r)
	if !ok {
		return
	}
	plan, err := s.backend.PlanMigration(r.Context(), req)
	if err != nil {
		jsonError(w, transferStatus(err), err.Error())
		return
	}
	jsonEncode(w, plan)
}

// handleMigrateExecute handles "execute a migration".
//
//	@Summary		Execute a migration
//	@Tags			transfer
//	@Produce		json
//	@x-crush-scope	"never"
//	@Router			/migrate/execute [post]
func (s *Server) handleMigrateExecute(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeBody[proto.MigrateExecuteRequest](w, r)
	if !ok {
		return
	}
	jsonEncode(w, s.backend.ExecuteMigration(r.Context(), req.Plan, req.Resume))
}
