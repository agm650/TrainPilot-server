package store

import (
	"context"
	"encoding/json"
	"time"
)

type AuditEvent struct {
	ID                                                       string
	OccurredAt                                               time.Time
	ActorType, ActorID, Action, TargetType, TargetID, Source string
	Details                                                  any
}

func (s *Store) AddAudit(ctx context.Context, e AuditEvent) error {
	b, err := json.Marshal(e.Details)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO audit_events(id,occurred_at,actor_type,actor_id,action,target_type,target_id,source,details_json) VALUES(?,?,?,?,?,?,?,?,?)`, e.ID, timeText(e.OccurredAt), e.ActorType, e.ActorID, e.Action, e.TargetType, e.TargetID, e.Source, string(b))
	return err
}
