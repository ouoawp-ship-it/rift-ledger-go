package app

import (
	"riftledger/internal/sqlite"
	"sync"
	"time"
)

type operationMonitor struct {
	mu           sync.Mutex
	StartedAt    int64   `json:"started_at"`
	LastSuccess  int64   `json:"last_success"`
	LastFailure  int64   `json:"last_failure"`
	FailedUpdate int64   `json:"failed_update"`
	Processed    int64   `json:"processed"`
	Failures     int64   `json:"failures"`
	State        string  `json:"state"`
	LastMS       float64 `json:"last_ms"`
	MaxMS        float64 `json:"max_ms"`
}
type BusinessHealth struct {
	State        string  `json:"state"`
	StartedAt    int64   `json:"started_at"`
	LastSuccess  int64   `json:"last_success"`
	LastFailure  int64   `json:"last_failure"`
	FailedUpdate int64   `json:"failed_update"`
	Processed    int64   `json:"processed"`
	Failures     int64   `json:"failures"`
	LastMS       float64 `json:"last_ms"`
	MaxMS        float64 `json:"max_ms"`
}

func (s *Service) businessSnapshot() BusinessHealth {
	m := &s.operations
	m.mu.Lock()
	defer m.mu.Unlock()
	return BusinessHealth{State: m.State, StartedAt: m.StartedAt, LastSuccess: m.LastSuccess, LastFailure: m.LastFailure, FailedUpdate: m.FailedUpdate, Processed: m.Processed, Failures: m.Failures, LastMS: m.LastMS, MaxMS: m.MaxMS}
}
func (s *Service) recordUpdate(id int64, start time.Time, err error) {
	m := &s.operations
	m.mu.Lock()
	defer m.mu.Unlock()
	m.LastMS = float64(time.Since(start).Microseconds()) / 1000
	if m.LastMS > m.MaxMS {
		m.MaxMS = m.LastMS
	}
	if err != nil {
		m.Failures++
		m.State = "error"
		m.LastFailure = now()
		m.FailedUpdate = id
		return
	}
	m.Processed++
	m.State = "healthy"
	m.LastSuccess = now()
	m.FailedUpdate = 0
}
func (s *Service) NotifyOutbox() {
	select {
	case s.outboxWake <- struct{}{}:
	default:
	}
}
func (s *Service) OutboxWake() <-chan struct{} { return s.outboxWake }

type QueueHealth struct {
	State       string `json:"state"`
	Pending     int64  `json:"pending"`
	Inflight    int64  `json:"inflight"`
	NeedsReview int64  `json:"needs_review"`
	OldestAge   int64  `json:"oldest_age"`
}

func readQueueHealth(tx *sqlite.Tx) (QueueHealth, error) {
	r, e := tx.One(`SELECT COALESCE(SUM(state='PENDING'),0) AS pending,COALESCE(SUM(state='INFLIGHT'),0) AS inflight,COALESCE(SUM(state IN ('FAILED','UNKNOWN')),0) AS review,COALESCE(MIN(CASE WHEN state IN ('PENDING','INFLIGHT') THEN created_at END),0) AS oldest FROM outbox WHERE state!='SENT'`)
	if e != nil {
		return QueueHealth{}, e
	}
	q := QueueHealth{State: "idle", Pending: r.Int("pending"), Inflight: r.Int("inflight"), NeedsReview: r.Int("review")}
	if r.Int("oldest") > 0 {
		q.OldestAge = now() - r.Int("oldest")
		if q.OldestAge < 0 {
			q.OldestAge = 0
		}
	}
	if q.Pending+q.Inflight > 0 {
		q.State = "working"
	}
	if q.OldestAge >= 60 {
		q.State = "delayed"
	}
	if q.NeedsReview > 0 {
		q.State = "blocked"
	}
	return q, nil
}
func (s *Service) Operations() (any, error) {
	bot, e := s.BotConnection()
	if e != nil {
		return nil, e
	}
	return map[string]any{"checked_at": now(), "receiver": bot, "business": bot.Business, "sender": bot.Sender, "database": s.DB.Metrics()}, nil
}
