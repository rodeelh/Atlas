package engine

import (
	"context"
	"strings"
	"sync"
	"time"
)

const (
	defaultMLXBatchWindow     = 12 * time.Millisecond
	defaultMLXMaxConcurrency  = 2
	defaultMLXSchedulerBuffer = 8
)

type MLXSchedulerStats struct {
	QueueDepth      int     `json:"queueDepth"`
	ActiveRequests  int     `json:"activeRequests"`
	MaxConcurrency  int     `json:"maxConcurrency"`
	BatchWindowMs   int     `json:"batchWindowMs"`
	LastBatchSize   int     `json:"lastBatchSize,omitempty"`
	TotalRequests   int64   `json:"totalRequests,omitempty"`
	TotalBatches    int64   `json:"totalBatches,omitempty"`
	AvgQueueWaitSec float64 `json:"avgQueueWaitSec,omitempty"`
}

type mlxSchedulerRequest struct {
	ready      chan struct{}
	enqueuedAt time.Time
}

type mlxRequestScheduler struct {
	mu            sync.Mutex
	queue         []*mlxSchedulerRequest
	active        int
	maxConcurrent int
	batchWindow   time.Duration
	lastBatchSize int
	totalRequests int64
	totalBatches  int64
	totalWait     time.Duration
	dispatchTimer *time.Timer
}

var mlxSchedulers sync.Map

func normalizeMLXSchedulerKey(baseURL string) string {
	key := strings.TrimSpace(strings.ToLower(strings.TrimRight(baseURL, "/")))
	if key == "" {
		return "http://127.0.0.1:11990/v1"
	}
	return key
}

func schedulerDefaults() (int, time.Duration) {
	return defaultMLXMaxConcurrency, defaultMLXBatchWindow
}

func getMLXScheduler(baseURL string) *mlxRequestScheduler {
	key := normalizeMLXSchedulerKey(baseURL)
	if s, ok := mlxSchedulers.Load(key); ok {
		return s.(*mlxRequestScheduler)
	}
	maxConcurrent, batchWindow := schedulerDefaults()
	s := &mlxRequestScheduler{
		maxConcurrent: maxConcurrent,
		batchWindow:   batchWindow,
	}
	actual, _ := mlxSchedulers.LoadOrStore(key, s)
	return actual.(*mlxRequestScheduler)
}

func AcquireMLXRequest(ctx context.Context, baseURL string) (func(), time.Duration, int, error) {
	s := getMLXScheduler(baseURL)
	req := &mlxSchedulerRequest{
		ready:      make(chan struct{}, 1),
		enqueuedAt: time.Now(),
	}
	s.enqueue(req)

	select {
	case <-req.ready:
		wait := time.Since(req.enqueuedAt)
		stats := s.Snapshot()
		return func() { s.release() }, wait, stats.LastBatchSize, nil
	case <-ctx.Done():
		if s.cancel(req) {
			return nil, 0, 0, ctx.Err()
		}
		select {
		case <-req.ready:
			s.release()
		default:
		}
		return nil, 0, 0, ctx.Err()
	}
}

func ConfigureMLXScheduler(baseURL string, maxConcurrency int, batchWindow time.Duration) MLXSchedulerStats {
	s := getMLXScheduler(baseURL)
	s.mu.Lock()
	if maxConcurrency <= 0 {
		maxConcurrency = 1
	}
	if batchWindow < 0 {
		batchWindow = 0
	}
	s.maxConcurrent = maxConcurrency
	s.batchWindow = batchWindow
	if len(s.queue) > 0 {
		s.scheduleDispatchLocked()
	}
	stats := s.snapshotLocked()
	s.mu.Unlock()
	return stats
}

func MLXSchedulerSnapshot(baseURL string) MLXSchedulerStats {
	return getMLXScheduler(baseURL).Snapshot()
}

func (s *mlxRequestScheduler) Snapshot() MLXSchedulerStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *mlxRequestScheduler) snapshotLocked() MLXSchedulerStats {
	stats := MLXSchedulerStats{
		QueueDepth:     len(s.queue),
		ActiveRequests: s.active,
		MaxConcurrency: s.maxConcurrent,
		BatchWindowMs:  int(s.batchWindow / time.Millisecond),
		LastBatchSize:  s.lastBatchSize,
		TotalRequests:  s.totalRequests,
		TotalBatches:   s.totalBatches,
	}
	if s.totalRequests > 0 {
		stats.AvgQueueWaitSec = s.totalWait.Seconds() / float64(s.totalRequests)
	}
	return stats
}

func (s *mlxRequestScheduler) enqueue(req *mlxSchedulerRequest) {
	s.mu.Lock()
	s.queue = append(s.queue, req)
	s.scheduleDispatchLocked()
	s.mu.Unlock()
}

func (s *mlxRequestScheduler) cancel(req *mlxSchedulerRequest) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, queued := range s.queue {
		if queued != req {
			continue
		}
		s.queue = append(s.queue[:i], s.queue[i+1:]...)
		return true
	}
	return false
}

func (s *mlxRequestScheduler) release() {
	s.mu.Lock()
	if s.active > 0 {
		s.active--
	}
	if len(s.queue) > 0 {
		s.scheduleDispatchLocked()
	}
	s.mu.Unlock()
}

func (s *mlxRequestScheduler) scheduleDispatchLocked() {
	if len(s.queue) == 0 || s.active >= s.maxConcurrent {
		return
	}
	if s.dispatchTimer != nil {
		return
	}
	wait := s.batchWindow
	if wait <= 0 {
		wait = time.Millisecond
	}
	s.dispatchTimer = time.AfterFunc(wait, s.dispatch)
}

func (s *mlxRequestScheduler) dispatch() {
	s.mu.Lock()
	s.dispatchTimer = nil
	available := s.maxConcurrent - s.active
	if available <= 0 || len(s.queue) == 0 {
		s.mu.Unlock()
		return
	}
	if available > defaultMLXSchedulerBuffer {
		available = defaultMLXSchedulerBuffer
	}
	if available > len(s.queue) {
		available = len(s.queue)
	}
	now := time.Now()
	batch := append([]*mlxSchedulerRequest(nil), s.queue[:available]...)
	s.queue = append([]*mlxSchedulerRequest(nil), s.queue[available:]...)
	s.active += len(batch)
	s.lastBatchSize = len(batch)
	s.totalBatches++
	s.totalRequests += int64(len(batch))
	for _, req := range batch {
		s.totalWait += now.Sub(req.enqueuedAt)
	}
	if len(s.queue) > 0 && s.active < s.maxConcurrent {
		s.scheduleDispatchLocked()
	}
	s.mu.Unlock()

	for _, req := range batch {
		req.ready <- struct{}{}
	}
}
