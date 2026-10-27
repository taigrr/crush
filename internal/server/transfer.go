package server

import (
	"errors"
	"net/http"

	"github.com/taigrr/crush/internal/backend"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/transfer"
)

func (s *Server) installTransferRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/sessions/move", s.handleMoveSession)
	mux.HandleFunc("POST /v1/transfer/preflight", s.handleTransferPreflight)
	mux.HandleFunc("POST /v1/transfer/import", s.handleTransferImport)
	mux.HandleFunc("POST /v1/migrate/plan", s.handleMigratePlan)
	mux.HandleFunc("POST /v1/migrate/execute", s.handleMigrateExecute)
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

func (s *Server) handleMigrateExecute(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeBody[proto.MigrateExecuteRequest](w, r)
	if !ok {
		return
	}
	jsonEncode(w, s.backend.ExecuteMigration(r.Context(), req.Plan, req.Resume))
}
