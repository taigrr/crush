package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/pubsub"
	"github.com/zeebo/xxh3"
)

type TodoStatus string

const (
	TodoStatusPending    TodoStatus = "pending"
	TodoStatusInProgress TodoStatus = "in_progress"
	TodoStatusCompleted  TodoStatus = "completed"
)

// HashID returns the XXH3 hash of a session ID (UUID) as a hex string.
func HashID(id string) string {
	h := xxh3.New()
	h.WriteString(id)
	return fmt.Sprintf("%x", h.Sum(nil))
}

type Todo struct {
	Content    string     `json:"content"`
	Status     TodoStatus `json:"status"`
	ActiveForm string     `json:"active_form"`
}

// HasIncompleteTodos returns true if there are any non-completed todos.
func HasIncompleteTodos(todos []Todo) bool {
	for _, todo := range todos {
		if todo.Status != TodoStatusCompleted {
			return true
		}
	}
	return false
}

type Session struct {
	ID               string
	ParentSessionID  string
	Title            string
	MessageCount     int64
	PromptTokens     int64
	CompletionTokens int64
	EstimatedUsage   bool
	SummaryMessageID string
	Cost             float64
	Todos            []Todo
	// WorkingDir is the directory the session was started from. Tools run
	// in this directory so a session resumed from a different client (with
	// a different launch cwd) still operates on the original project.
	WorkingDir string
	// LastFinishedAt is the unix time of the session's most recent run
	// completion; LastSeenAt is the unix time the viewing client last
	// opened it. Unread is LastFinishedAt > LastSeenAt.
	LastFinishedAt int64
	LastSeenAt     int64
	CreatedAt      int64
	UpdatedAt      int64
	ArchivedAt     int64

	// Color and Animal form the session's swarm identity: a human
	// readable address (e.g. "aliceblue" + "tiger") derived
	// deterministically from the session id via the configured
	// colorhash palette and animal list. They are backfilled at
	// startup for legacy rows and reset on every Get so old cached
	// values in memory stay authoritative even after DB writes
	// elsewhere.
	Color  string
	Animal string

	// Favorite pins the session to the top of the sidebar inbox (just
	// below sessions blocked on a permission prompt) so an orchestrator
	// session controlling swarm workers is easy to return to.
	Favorite bool

	// Model is the session's own model selection, when it has one. A
	// human-opened session is stamped with the configured orchestrator
	// model at creation; a swarm worker or sub-agent child is stamped
	// only when its creator passed an explicit model. Nil means "resolve
	// to the workspace's large model at run time", which is how every
	// session behaved before per-session models existed. Only Provider,
	// Model, ReasoningEffort, and Think are persisted; the remaining
	// tuning fields are filled from the catalog when the run resolves it.
	Model *config.SelectedModel
}

// ModelOrNil returns a copy of the session's own selection, or nil.
// Convenience for callers that want value semantics without aliasing
// the stored pointer.
func (s Session) ModelOrNil() *config.SelectedModel {
	if s.Model == nil {
		return nil
	}
	m := *s.Model
	return &m
}

// Unread reports whether the session finished a run more recently than it
// was last opened, i.e. it has completed work the viewer has not seen.
func (s Session) Unread() bool {
	return s.LastFinishedAt > 0 && s.LastFinishedAt > s.LastSeenAt
}

type Service interface {
	pubsub.Subscriber[Session]
	Create(ctx context.Context, title string) (Session, error)
	// CreateWithModel creates a top-level session stamped with its own
	// model selection. A nil model behaves exactly like Create.
	CreateWithModel(ctx context.Context, title string, model *config.SelectedModel) (Session, error)
	CreateTitleSession(ctx context.Context, parentSessionID string) (Session, error)
	CreateTaskSession(ctx context.Context, toolCallID, parentSessionID, title string) (Session, error)
	// CreateTaskSessionWithModel is CreateTaskSession with a per-session
	// model stamp for the child. A nil model behaves like
	// CreateTaskSession.
	CreateTaskSessionWithModel(ctx context.Context, toolCallID, parentSessionID, title string, model *config.SelectedModel) (Session, error)
	// SetModel stamps (non-nil) or clears (nil) the session's own model
	// selection and publishes an update so viewers re-render.
	SetModel(ctx context.Context, id string, model *config.SelectedModel) error
	Get(ctx context.Context, id string) (Session, error)
	GetLast(ctx context.Context) (Session, error)
	List(ctx context.Context) ([]Session, error)
	ListArchived(ctx context.Context) ([]Session, error)
	Save(ctx context.Context, session Session) (Session, error)
	UpdateTitleAndUsage(ctx context.Context, sessionID, title string, promptTokens, completionTokens int64, cost float64) error
	Rename(ctx context.Context, id string, title string) error
	Archive(ctx context.Context, id string) error
	Unarchive(ctx context.Context, id string) error
	Delete(ctx context.Context, id string) error

	// SetWorkingDir records the directory the session runs its tools in.
	SetWorkingDir(ctx context.Context, id, dir string) error
	// SetFavorite pins or unpins a session so the sidebar inbox sticks it
	// to the top. Publishes an update so the sidebar reprojects.
	SetFavorite(ctx context.Context, id string, favorite bool) error
	// SetSwarmIdentity stores the color/animal pair used by the swarm
	// tool to address the session across workspaces. Idempotent.
	SetSwarmIdentity(ctx context.Context, id, color, animal string) error
	// FindByColorAnimal returns every session that matches the
	// color/animal pair. Callers disambiguate collisions using the
	// short session-id suffix. Never returns sub-sessions (title,
	// summary, task tool) because the swarm tool refuses to send to
	// them anyway; the caller filters after this returns.
	FindByColorAnimal(ctx context.Context, color, animal string) ([]Session, error)
	// MarkFinished stamps the session's most recent run completion time.
	MarkFinished(ctx context.Context, id string) error
	// MarkSeen stamps the time the viewing client last opened the session,
	// clearing its unread state.
	MarkSeen(ctx context.Context, id string) error
	// NotifyImported publishes a Created or Updated event for a session
	// that was written outside this service (the importer). Created is
	// used on first import so swarm identity assignment and the sidebar
	// pick the new row up; Updated is used on a re-sync.
	NotifyImported(ctx context.Context, id string, created bool)

	// Agent tool session management
	CreateAgentToolSessionID(messageID, toolCallID string) string
	ParseAgentToolSessionID(sessionID string) (messageID string, toolCallID string, ok bool)
	IsAgentToolSession(sessionID string) bool
}

type service struct {
	*pubsub.Broker[Session]
	db *sql.DB
	q  *db.Queries

	// Estimated usage stays in memory so fetch-modify-save paths (e.g.,
	// updating todos or parent-session cost) do not rebuild a session from
	// SQLite and incorrectly clear the UI "~" marker.
	estimatedUsageMu sync.RWMutex
	estimatedUsage   map[string]bool
}

func (s *service) Create(ctx context.Context, title string) (Session, error) {
	return s.CreateWithModel(ctx, title, nil)
}

func (s *service) CreateWithModel(ctx context.Context, title string, model *config.SelectedModel) (Session, error) {
	return s.create(ctx, db.CreateSessionParams{
		ID:    uuid.New().String(),
		Title: title,
	}, model)
}

func (s *service) CreateTaskSession(ctx context.Context, toolCallID, parentSessionID, title string) (Session, error) {
	return s.CreateTaskSessionWithModel(ctx, toolCallID, parentSessionID, title, nil)
}

func (s *service) CreateTaskSessionWithModel(ctx context.Context, toolCallID, parentSessionID, title string, model *config.SelectedModel) (Session, error) {
	return s.create(ctx, db.CreateSessionParams{
		ID:              toolCallID,
		ParentSessionID: sql.NullString{String: parentSessionID, Valid: true},
		Title:           title,
	}, model)
}

// create inserts the row and, when a model is given, stamps it in the
// same call before publishing Created. Stamping before publish matters:
// subscribers (sidebar, swarm identity backfill) see one consistent row
// rather than a Created without a model followed by an Updated with one.
func (s *service) create(ctx context.Context, params db.CreateSessionParams, model *config.SelectedModel) (Session, error) {
	dbSession, err := s.q.CreateSession(ctx, params)
	if err != nil {
		return Session{}, err
	}
	if model != nil && model.Provider != "" && model.Model != "" {
		if err := s.q.SetSessionModel(ctx, sessionModelParams(dbSession.ID, model)); err != nil {
			return Session{}, fmt.Errorf("stamping session model: %w", err)
		}
		dbSession, err = s.q.GetSessionByID(ctx, dbSession.ID)
		if err != nil {
			return Session{}, err
		}
	}
	session := s.fromDBItem(dbSession)
	s.Publish(pubsub.CreatedEvent, session)
	return session, nil
}

func (s *service) SetModel(ctx context.Context, id string, model *config.SelectedModel) error {
	if err := s.q.SetSessionModel(ctx, sessionModelParams(id, model)); err != nil {
		return fmt.Errorf("setting session model: %w", err)
	}
	s.publishUpdate(ctx, id)
	return nil
}

// sessionModelParams maps a selection onto the nullable columns. A nil
// or incomplete selection clears all three so the row falls back to the
// workspace large model.
func sessionModelParams(id string, model *config.SelectedModel) db.SetSessionModelParams {
	p := db.SetSessionModelParams{ID: id}
	if model == nil || model.Provider == "" || model.Model == "" {
		return p
	}
	p.ModelProvider = sql.NullString{String: model.Provider, Valid: true}
	p.ModelID = sql.NullString{String: model.Model, Valid: true}
	p.ModelReasoningEffort = sql.NullString{String: model.ReasoningEffort, Valid: model.ReasoningEffort != ""}
	var think int64
	if model.Think {
		think = 1
	}
	p.ModelThink = sql.NullInt64{Int64: think, Valid: true}
	return p
}

func (s *service) CreateTitleSession(ctx context.Context, parentSessionID string) (Session, error) {
	dbSession, err := s.q.CreateSession(ctx, db.CreateSessionParams{
		ID:              "title-" + parentSessionID,
		ParentSessionID: sql.NullString{String: parentSessionID, Valid: true},
		Title:           "Generate a title",
	})
	if err != nil {
		return Session{}, err
	}
	session := s.fromDBItem(dbSession)
	s.Publish(pubsub.CreatedEvent, session)
	return session, nil
}

func (s *service) Delete(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	qtx := s.q.WithTx(tx)

	dbSession, err := qtx.GetSessionByID(ctx, id)
	if err != nil {
		return err
	}
	if err = qtx.DeleteSessionMessages(ctx, dbSession.ID); err != nil {
		return fmt.Errorf("deleting session messages: %w", err)
	}
	if err = qtx.DeleteSessionFiles(ctx, dbSession.ID); err != nil {
		return fmt.Errorf("deleting session files: %w", err)
	}
	if err = qtx.DeleteSession(ctx, dbSession.ID); err != nil {
		return fmt.Errorf("deleting session: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}

	session := s.fromDBItem(dbSession)
	s.clearEstimatedUsageState(dbSession.ID)
	s.Publish(pubsub.DeletedEvent, session)
	return nil
}

func (s *service) Get(ctx context.Context, id string) (Session, error) {
	dbSession, err := s.q.GetSessionByID(ctx, id)
	if err != nil {
		return Session{}, err
	}
	session := s.fromDBItem(dbSession)
	s.applyEstimatedUsageState(&session)
	return session, nil
}

func (s *service) GetLast(ctx context.Context) (Session, error) {
	dbSession, err := s.q.GetLastSession(ctx)
	if err != nil {
		return Session{}, err
	}
	session := s.fromDBItem(dbSession)
	s.applyEstimatedUsageState(&session)
	return session, nil
}

func (s *service) Save(ctx context.Context, session Session) (Session, error) {
	todosJSON, err := marshalTodos(session.Todos)
	if err != nil {
		return Session{}, err
	}

	dbSession, err := s.q.UpdateSession(ctx, db.UpdateSessionParams{
		ID:               session.ID,
		Title:            session.Title,
		PromptTokens:     session.PromptTokens,
		CompletionTokens: session.CompletionTokens,
		SummaryMessageID: sql.NullString{
			String: session.SummaryMessageID,
			Valid:  session.SummaryMessageID != "",
		},
		Cost: session.Cost,
		Todos: sql.NullString{
			String: todosJSON,
			Valid:  todosJSON != "",
		},
	})
	if err != nil {
		return Session{}, err
	}
	estimatedUsage := session.EstimatedUsage
	s.setEstimatedUsageState(session.ID, estimatedUsage)
	session = s.fromDBItem(dbSession)
	session.EstimatedUsage = estimatedUsage
	s.Publish(pubsub.UpdatedEvent, session)
	return session, nil
}

// UpdateTitleAndUsage updates only the title and usage fields atomically.
// This is safer than fetching, modifying, and saving the entire session.
func (s *service) UpdateTitleAndUsage(ctx context.Context, sessionID, title string, promptTokens, completionTokens int64, cost float64) error {
	if err := s.q.UpdateSessionTitleAndUsage(ctx, db.UpdateSessionTitleAndUsageParams{
		ID:               sessionID,
		Title:            title,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		Cost:             cost,
	}); err != nil {
		return err
	}
	s.publishUpdate(ctx, sessionID)
	return nil
}

// Rename updates only the title of a session without touching updated_at or
// usage fields.
func (s *service) Rename(ctx context.Context, id string, title string) error {
	if err := s.q.RenameSession(ctx, db.RenameSessionParams{
		ID:    id,
		Title: title,
	}); err != nil {
		return err
	}
	s.publishUpdate(ctx, id)
	return nil
}

// publishUpdate fetches the session and publishes an UpdatedEvent so
// subscribers (the UI, over SSE) observe title changes made by the
// title-only update paths that bypass Save.
func (s *service) publishUpdate(ctx context.Context, id string) {
	dbSession, err := s.q.GetSessionByID(ctx, id)
	if err != nil {
		return
	}
	session := s.fromDBItem(dbSession)
	s.applyEstimatedUsageState(&session)
	s.Publish(pubsub.UpdatedEvent, session)
}

func (s *service) List(ctx context.Context) ([]Session, error) {
	dbSessions, err := s.q.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	sessions := make([]Session, len(dbSessions))
	for i, dbSession := range dbSessions {
		sessions[i] = s.fromDBItem(dbSession)
		s.applyEstimatedUsageState(&sessions[i])
	}
	return sessions, nil
}

func (s *service) ListArchived(ctx context.Context) ([]Session, error) {
	dbSessions, err := s.q.ListArchivedSessions(ctx)
	if err != nil {
		return nil, err
	}
	sessions := make([]Session, len(dbSessions))
	for i, dbSession := range dbSessions {
		sessions[i] = s.fromDBItem(dbSession)
	}
	return sessions, nil
}

func (s *service) Archive(ctx context.Context, id string) error {
	if err := s.q.ArchiveSession(ctx, id); err != nil {
		return fmt.Errorf("archiving session: %w", err)
	}
	dbSession, err := s.q.GetSessionByID(ctx, id)
	if err != nil {
		return fmt.Errorf("getting archived session: %w", err)
	}
	s.Publish(pubsub.UpdatedEvent, s.fromDBItem(dbSession))
	return nil
}

func (s *service) Unarchive(ctx context.Context, id string) error {
	if err := s.q.UnarchiveSession(ctx, id); err != nil {
		return fmt.Errorf("unarchiving session: %w", err)
	}
	dbSession, err := s.q.GetSessionByID(ctx, id)
	if err != nil {
		return fmt.Errorf("getting unarchived session: %w", err)
	}
	s.Publish(pubsub.UpdatedEvent, s.fromDBItem(dbSession))
	return nil
}

func (s *service) SetSwarmIdentity(ctx context.Context, id, color, animal string) error {
	rows, err := s.q.SetSessionSwarmIdentity(ctx, db.SetSessionSwarmIdentityParams{
		ID:     id,
		Color:  sql.NullString{String: color, Valid: color != ""},
		Animal: sql.NullString{String: animal, Valid: animal != ""},
	})
	if err != nil {
		return fmt.Errorf("setting session swarm identity: %w", err)
	}
	// The SQL WHERE clause skips rows that already have an
	// identity, so parallel writers (startup backfill + Created
	// subscriber) don't clobber each other and don't churn pubsub
	// with redundant Update events. A no-op UPDATE is either a
	// legitimate race (both writers arrived) or a missing session
	// id; the caller cannot distinguish these from success, which
	// is fine for the current call sites (all pass freshly-listed
	// rows) but is worth logging at debug so config drift or
	// missing-id bugs are diagnosable.
	if rows == 0 {
		slog.Debug("SetSwarmIdentity was a no-op",
			"session_id", id, "color", color, "animal", animal)
		return nil
	}
	s.publishByID(ctx, id)
	return nil
}

func (s *service) FindByColorAnimal(ctx context.Context, color, animal string) ([]Session, error) {
	dbSessions, err := s.q.FindSessionsByColorAnimal(ctx, db.FindSessionsByColorAnimalParams{
		Color:  sql.NullString{String: color, Valid: color != ""},
		Animal: sql.NullString{String: animal, Valid: animal != ""},
	})
	if err != nil {
		return nil, err
	}
	out := make([]Session, len(dbSessions))
	for i, dbSession := range dbSessions {
		out[i] = s.fromDBItem(dbSession)
		s.applyEstimatedUsageState(&out[i])
	}
	return out, nil
}

func (s *service) SetWorkingDir(ctx context.Context, id, dir string) error {
	if dir == "" {
		return nil
	}
	if err := s.q.SetSessionWorkingDir(ctx, db.SetSessionWorkingDirParams{
		WorkingDir: sql.NullString{String: dir, Valid: true},
		ID:         id,
	}); err != nil {
		return fmt.Errorf("setting session working dir: %w", err)
	}
	return nil
}

func (s *service) SetFavorite(ctx context.Context, id string, favorite bool) error {
	fav := int64(0)
	if favorite {
		fav = 1
	}
	if err := s.q.SetSessionFavorite(ctx, db.SetSessionFavoriteParams{
		ID:       id,
		Favorite: fav,
	}); err != nil {
		return fmt.Errorf("setting session favorite: %w", err)
	}
	s.publishByID(ctx, id)
	return nil
}

func (s *service) MarkFinished(ctx context.Context, id string) error {
	if err := s.q.MarkSessionFinished(ctx, id); err != nil {
		return fmt.Errorf("marking session finished: %w", err)
	}
	s.publishByID(ctx, id)
	return nil
}

func (s *service) MarkSeen(ctx context.Context, id string) error {
	if err := s.q.MarkSessionSeen(ctx, id); err != nil {
		return fmt.Errorf("marking session seen: %w", err)
	}
	s.publishByID(ctx, id)
	return nil
}

// publishByID re-reads a session and publishes an update so subscribers
// (e.g. the sessions sidebar) observe read/unread state changes.
func (s *service) publishByID(ctx context.Context, id string) {
	dbSession, err := s.q.GetSessionByID(ctx, id)
	if err != nil {
		return
	}
	s.Publish(pubsub.UpdatedEvent, s.fromDBItem(dbSession))
}

func (s *service) NotifyImported(ctx context.Context, id string, created bool) {
	dbSession, err := s.q.GetSessionByID(ctx, id)
	if err != nil {
		return
	}
	event := pubsub.UpdatedEvent
	if created {
		event = pubsub.CreatedEvent
	}
	s.Publish(event, s.fromDBItem(dbSession))
}

func (s *service) applyEstimatedUsageState(session *Session) {
	s.estimatedUsageMu.RLock()
	session.EstimatedUsage = s.estimatedUsage[session.ID]
	s.estimatedUsageMu.RUnlock()
}

func (s *service) setEstimatedUsageState(sessionID string, estimatedUsage bool) {
	s.estimatedUsageMu.Lock()
	defer s.estimatedUsageMu.Unlock()
	if estimatedUsage {
		s.estimatedUsage[sessionID] = true
		return
	}
	delete(s.estimatedUsage, sessionID)
}

func (s *service) clearEstimatedUsageState(sessionID string) {
	s.estimatedUsageMu.Lock()
	delete(s.estimatedUsage, sessionID)
	s.estimatedUsageMu.Unlock()
}

func (s *service) fromDBItem(item db.Session) Session {
	todos, err := unmarshalTodos(item.Todos.String)
	if err != nil {
		slog.Error("Failed to unmarshal todos", "session_id", item.ID, "error", err)
	}
	return Session{
		ID:               item.ID,
		ParentSessionID:  item.ParentSessionID.String,
		Title:            item.Title,
		MessageCount:     item.MessageCount,
		PromptTokens:     item.PromptTokens,
		CompletionTokens: item.CompletionTokens,
		SummaryMessageID: item.SummaryMessageID.String,
		Cost:             item.Cost,
		Todos:            todos,
		WorkingDir:       item.WorkingDir.String,
		LastFinishedAt:   item.LastFinishedAt.Int64,
		LastSeenAt:       item.LastSeenAt.Int64,
		CreatedAt:        item.CreatedAt,
		UpdatedAt:        item.UpdatedAt,
		ArchivedAt:       item.ArchivedAt.Int64,
		Color:            item.Color.String,
		Animal:           item.Animal.String,
		Favorite:         item.Favorite != 0,
		Model:            modelFromDBItem(item),
	}
}

// modelFromDBItem rebuilds the session's own selection from the nullable
// columns. Both provider and id must be present; a half-written row is
// treated as unset rather than producing an unresolvable selection.
func modelFromDBItem(item db.Session) *config.SelectedModel {
	if !item.ModelProvider.Valid || !item.ModelID.Valid ||
		item.ModelProvider.String == "" || item.ModelID.String == "" {
		return nil
	}
	return &config.SelectedModel{
		Provider:        item.ModelProvider.String,
		Model:           item.ModelID.String,
		ReasoningEffort: item.ModelReasoningEffort.String,
		Think:           item.ModelThink.Valid && item.ModelThink.Int64 != 0,
	}
}

func marshalTodos(todos []Todo) (string, error) {
	if len(todos) == 0 {
		return "", nil
	}
	data, err := json.Marshal(todos)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func unmarshalTodos(data string) ([]Todo, error) {
	if data == "" {
		return []Todo{}, nil
	}
	var todos []Todo
	if err := json.Unmarshal([]byte(data), &todos); err != nil {
		return []Todo{}, err
	}
	return todos, nil
}

func NewService(q *db.Queries, conn *sql.DB) Service {
	broker := pubsub.NewBroker[Session]()
	return &service{
		Broker:         broker,
		db:             conn,
		q:              q,
		estimatedUsage: make(map[string]bool),
	}
}

// CreateAgentToolSessionID creates a session ID for agent tool sessions using the format "messageID$$toolCallID"
func (s *service) CreateAgentToolSessionID(messageID, toolCallID string) string {
	return fmt.Sprintf("%s$$%s", messageID, toolCallID)
}

// ParseAgentToolSessionID parses an agent tool session ID into its components
func (s *service) ParseAgentToolSessionID(sessionID string) (messageID string, toolCallID string, ok bool) {
	parts := strings.Split(sessionID, "$$")
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// IsAgentToolSession checks if a session ID follows the agent tool session format
func (s *service) IsAgentToolSession(sessionID string) bool {
	_, _, ok := s.ParseAgentToolSessionID(sessionID)
	return ok
}
