// Package cron provides a thin wrapper around robfig/cron for schedule-based triggers.
package cron

import (
	"context"
	"sync"

	"github.com/robfig/cron/v3"
)

// Scheduler manages cron-based schedule triggers.
// Each cron rule fires a callback that creates a new Run.
type Scheduler struct {
	mu   sync.Mutex
	cron *cron.Cron
}

// NewScheduler creates a new Scheduler.
func NewScheduler() *Scheduler {
	return &Scheduler{
		cron: cron.New(cron.WithParser(cronParser)),
	}
}

// AddRule registers a cron expression with a callback.
func (s *Scheduler) AddRule(cronExpr string, fn func()) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.cron.AddFunc(cronExpr, fn)
	return err
}

// Start begins the cron scheduler.
func (s *Scheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cron.Start()
}

// Stop halts the scheduler and waits for running jobs to complete.
func (s *Scheduler) Stop() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cron.Stop()
}

// cronParser is a compatibility-mode parser that supports:
// 1. Standard 5-field: "0 9 * * *" (daily at 9:00:00)
// 2. Optional seconds (6-field): "30 0 9 * * *" (daily at 9:00:30)
// 3. Descriptors: "@every 10s", "@daily"
var cronParser = cron.NewParser(
	cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)
