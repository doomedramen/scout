package topology

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"scout.local/scout/internal/store"
)

type Evidence struct {
	ID          string
	FromEntity  string
	ToEntity    string
	Type        string
	Confidence  float64
	Source      string
	ObservedAt  time.Time
	ExpiresAt   time.Time
	EvidenceIDs []string
}

type Reconciler struct {
	Store *store.Store
	Now   func() time.Time
}

func (r *Reconciler) clock() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

// Apply projects fresh evidence without merging inventory identities. An
// address match alone remains a low-confidence relationship and never changes
// device ownership; owner corrections and revoked tombstones stay separate.
func (r *Reconciler) Apply(ctx context.Context, evidence []Evidence) ([]store.Relationship, error) {
	if r == nil || r.Store == nil {
		return nil, store.ErrInvalid
	}
	result := []store.Relationship{}
	for _, item := range evidence {
		if strings.TrimSpace(item.FromEntity) == "" || strings.TrimSpace(item.ToEntity) == "" || item.FromEntity == item.ToEntity || strings.TrimSpace(item.Type) == "" || item.Confidence < 0 || item.Confidence > 1 {
			return nil, errors.New("invalid topology evidence")
		}
		from, fromErr := r.Store.GetDevice(ctx, item.FromEntity)
		to, toErr := r.Store.GetDevice(ctx, item.ToEntity)
		if fromErr != nil || toErr != nil {
			return nil, store.ErrNotFound
		}
		if from.Lifecycle == "decommissioned" || to.Lifecycle == "decommissioned" {
			continue
		}
		observed := item.ObservedAt
		if observed.IsZero() {
			observed = r.clock()
		}
		expires := item.ExpiresAt
		if expires.IsZero() {
			expires = observed.Add(15 * time.Minute)
		}
		typeName := item.Type
		if item.Source == "address-only" && item.Confidence > 0.5 {
			item.Confidence = 0.5
		}
		relationship, err := r.Store.UpsertRelationship(ctx, store.Relationship{ID: item.ID, FromEntity: from.ID, ToEntity: to.ID, Type: typeName, Confidence: item.Confidence, ProjectionRevision: max(from.Revision, to.Revision), EvidenceIDs: item.EvidenceIDs, ObservedAt: observed, ExpiresAt: expires, Source: item.Source})
		if err != nil {
			return nil, err
		}
		result = append(result, relationship)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ObservedAt.Before(result[j].ObservedAt) })
	return result, nil
}

func (r *Reconciler) Expire(ctx context.Context) (int, error) {
	if r == nil || r.Store == nil {
		return 0, store.ErrInvalid
	}
	now := r.clock()
	items, err := r.Store.AllRelationships(ctx)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, item := range items {
		if !item.ExpiresAt.IsZero() && !now.Before(item.ExpiresAt) && item.Source != "owner-correction" {
			if err := r.Store.RemoveRelationship(ctx, item.ID); err != nil {
				return removed, err
			}
			removed++
		}
	}
	return removed, nil
}

func max(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
