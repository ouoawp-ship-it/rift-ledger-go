package telegram

import (
	"context"
	"sync"
	"time"
)

// Shared by all workers of one bot. Durable outbox claims preserve order;
// this clock adds sub-second precision and caps the aggregate sending rate.
type sendScheduler struct {
	mu     sync.Mutex
	global time.Time
	chats  map[int64]time.Time
}

func newSendScheduler() *sendScheduler { return &sendScheduler{chats: map[int64]time.Time{}} }
func (s *sendScheduler) reserve(chat int64, at time.Time) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	due := at
	if s.global.After(due) {
		due = s.global
	}
	if s.chats[chat].After(due) {
		due = s.chats[chat]
	}
	// A throttled group must not reserve future global slots and delay other chats.
	if due.After(at) {
		return due.Sub(at)
	}
	interval := time.Second
	if chat < 0 {
		interval = 3 * time.Second
	}
	s.global = due.Add(50 * time.Millisecond)
	s.chats[chat] = due.Add(interval)
	if len(s.chats) > 2048 {
		for id, until := range s.chats {
			if until.Before(at) {
				delete(s.chats, id)
			}
		}
	}
	return due.Sub(at)
}
func (s *sendScheduler) wait(ctx context.Context, chat int64) bool {
	for {
		if ctx.Err() != nil {
			return false
		}
		delay := s.reserve(chat, time.Now())
		if delay <= 0 {
			return true
		}
		if !wait(ctx, delay) {
			return false
		}
	}
}
func waitForOutbox(ctx context.Context, sig <-chan struct{}) bool {
	timer := time.NewTimer(500 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-sig:
		return true
	case <-timer.C:
		return true
	}
}
