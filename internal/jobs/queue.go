package jobs

import (
	"context"
	"time"

	"scout.local/scout/internal/store"
)

type Queue struct{ Store *store.Store }

func (q Queue) Enqueue(ctx context.Context, job store.Job) (store.Job, error) {
	if job.Kind == "" {
		job.Kind = "enrollment"
	}
	return q.Store.CreateJob(ctx, job)
}
func (q Queue) Claim(ctx context.Context, worker string) (store.Job, error) {
	return q.Store.ClaimJob(ctx, worker)
}
func (q Queue) Renew(ctx context.Context, job store.Job) (store.Job, error) {
	return q.Store.RenewJob(ctx, job.ID, job.LeaseOwner, job.Epoch)
}
func (q Queue) Progress(ctx context.Context, job store.Job, state string, result map[string]string) error {
	return q.Store.ReportJob(ctx, job.ID, job.LeaseOwner, job.Epoch, state, result)
}
func (q Queue) RunLease(ctx context.Context, job store.Job, interval time.Duration, fn func(context.Context, store.Job) error) error {
	if interval <= 0 {
		interval = 20 * time.Second
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- fn(workCtx, job) }()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var expiry <-chan time.Time
	var timer *time.Timer
	resetExpiry := func(value *time.Time) {
		if timer != nil {
			timer.Stop()
		}
		if value == nil {
			expiry = nil
			return
		}
		delay := time.Until(*value)
		if delay < 0 {
			delay = 0
		}
		timer = time.NewTimer(delay)
		expiry = timer.C
	}
	resetExpiry(job.LeaseExpiry)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-result:
			return err
		case <-ticker.C:
			renewed, err := q.Renew(ctx, job)
			if err != nil {
				return err
			}
			job = renewed
			resetExpiry(job.LeaseExpiry)
		case <-expiry:
			return store.ErrConflict
		}
	}
}
