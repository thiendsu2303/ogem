package schema

import (
	"context"
	"log"
	"time"
)

// Scheduler handles periodic schema monitoring
type Scheduler struct {
	monitor    *Monitor
	interval   time.Duration
	stopChan   chan struct{}
	isRunning  bool
}

// NewScheduler creates a new scheduler with the given monitor and check interval
func NewScheduler(monitor *Monitor, interval time.Duration) *Scheduler {
	return &Scheduler{
		monitor:   monitor,
		interval:  interval,
		stopChan:  make(chan struct{}),
	}
}

// Start begins the periodic schema monitoring
func (s *Scheduler) Start(ctx context.Context) {
	if s.isRunning {
		return
	}
	s.isRunning = true

	go func() {
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		// Run initial check
		if err := s.monitor.CheckOpenAISchema(ctx); err != nil {
			log.Printf("Error checking OpenAI schema: %v", err)
		}

		for {
			select {
			case <-ticker.C:
				if err := s.monitor.CheckOpenAISchema(ctx); err != nil {
					log.Printf("Error checking OpenAI schema: %v", err)
				}
			case <-s.stopChan:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Stop halts the periodic schema monitoring
func (s *Scheduler) Stop() {
	if !s.isRunning {
		return
	}
	s.stopChan <- struct{}{}
	s.isRunning = false
}
