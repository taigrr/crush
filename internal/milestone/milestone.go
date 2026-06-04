// Package milestone provides a service for managing conversation
// milestones — periodic short summaries that serve as a "minimap" of
// what has happened in a session.
package milestone

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/pubsub"
)

// Milestone represents a single periodic summary entry for a session.
type Milestone struct {
	ID           string
	SessionID    string
	TurnNumber   int64
	ShortSummary string // 5-8 word summary.
	FullSummary  string // 2-3 sentence summary.
	CreatedAt    int64
}

// Service provides CRUD operations for milestones.
type Service interface {
	pubsub.Subscriber[Milestone]
	Create(ctx context.Context, sessionID string, turnNumber int64, shortSummary, fullSummary string) (Milestone, error)
	List(ctx context.Context, sessionID string) ([]Milestone, error)
	Latest(ctx context.Context, sessionID string) (Milestone, error)
	Count(ctx context.Context, sessionID string) (int64, error)
	DeleteBySession(ctx context.Context, sessionID string) error
}

type service struct {
	*pubsub.Broker[Milestone]
	q *db.Queries
}

// NewService creates a new milestone service.
func NewService(sqlDB *sql.DB, q *db.Queries) Service {
	return &service{
		Broker: pubsub.NewBroker[Milestone](),
		q:      q,
	}
}

func (s *service) Create(ctx context.Context, sessionID string, turnNumber int64, shortSummary, fullSummary string) (Milestone, error) {
	now := time.Now().UnixMilli()
	dbMilestone, err := s.q.CreateMilestone(ctx, db.CreateMilestoneParams{
		ID:           uuid.New().String(),
		SessionID:    sessionID,
		TurnNumber:   turnNumber,
		ShortSummary: shortSummary,
		FullSummary:  fullSummary,
		CreatedAt:    now,
	})
	if err != nil {
		return Milestone{}, err
	}
	m := fromDB(dbMilestone)
	s.Publish(pubsub.CreatedEvent, m)
	return m, nil
}

func (s *service) List(ctx context.Context, sessionID string) ([]Milestone, error) {
	dbMilestones, err := s.q.ListMilestonesBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	milestones := make([]Milestone, len(dbMilestones))
	for i, m := range dbMilestones {
		milestones[i] = fromDB(m)
	}
	return milestones, nil
}

func (s *service) Latest(ctx context.Context, sessionID string) (Milestone, error) {
	dbMilestone, err := s.q.GetLatestMilestone(ctx, sessionID)
	if err != nil {
		return Milestone{}, err
	}
	return fromDB(dbMilestone), nil
}

func (s *service) Count(ctx context.Context, sessionID string) (int64, error) {
	return s.q.CountMilestonesBySession(ctx, sessionID)
}

func (s *service) DeleteBySession(ctx context.Context, sessionID string) error {
	return s.q.DeleteMilestonesBySession(ctx, sessionID)
}

func fromDB(m db.Milestone) Milestone {
	return Milestone{
		ID:           m.ID,
		SessionID:    m.SessionID,
		TurnNumber:   m.TurnNumber,
		ShortSummary: m.ShortSummary,
		FullSummary:  m.FullSummary,
		CreatedAt:    m.CreatedAt,
	}
}
