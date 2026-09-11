package collector

import (
	"context"
	"sync"
	"time"
)

type Scheduler struct {
	Registry      *Registry
	MaxConcurrent int
	Now           func() time.Time
}

func (s *Scheduler) RunOnce(ctx context.Context, onResult func(string, ServiceResult, error)) {
	if s == nil || s.Registry == nil {
		return
	}
	entries := s.Registry.Descriptors()
	limit := s.MaxConcurrent
	if limit <= 0 || limit > len(entries) {
		limit = len(entries)
	}
	if limit == 0 {
		return
	}
	semaphore := make(chan struct{}, limit)
	var group sync.WaitGroup
	for _, descriptor := range entries {
		descriptor := descriptor
		group.Add(1)
		go func() {
			defer group.Done()
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				if onResult != nil {
					onResult(descriptor.ID, ServiceResult{}, ctx.Err())
				}
				return
			}
			defer func() { <-semaphore }()
			if err := ctx.Err(); err != nil {
				if onResult != nil {
					onResult(descriptor.ID, ServiceResult{}, err)
				}
				return
			}
			result, err := s.Registry.Collect(ctx, descriptor.ID)
			if onResult != nil {
				onResult(descriptor.ID, result, err)
			}
		}()
	}
	group.Wait()
}

func (s *Scheduler) Run(ctx context.Context, onResult func(string, ServiceResult, error)) error {
	if s == nil || s.Registry == nil {
		return context.Canceled
	}
	interval := 30 * time.Second
	if descriptors := s.Registry.Descriptors(); len(descriptors) > 0 && descriptors[0].Interval > 0 {
		interval = descriptors[0].Interval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	s.RunOnce(ctx, onResult)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			s.RunOnce(ctx, onResult)
		}
	}
}
