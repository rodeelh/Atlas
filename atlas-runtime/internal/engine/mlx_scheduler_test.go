package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMLXSchedulerSerialMode(t *testing.T) {
	baseURL := "http://127.0.0.1:25001/v1"
	ConfigureMLXScheduler(baseURL, 1, 0)

	var active int32
	var peak int32
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, _, _, err := AcquireMLXRequest(context.Background(), baseURL)
			if err != nil {
				t.Errorf("AcquireMLXRequest: %v", err)
				return
			}
			cur := atomic.AddInt32(&active, 1)
			if cur > atomic.LoadInt32(&peak) {
				atomic.StoreInt32(&peak, cur)
			}
			time.Sleep(25 * time.Millisecond)
			atomic.AddInt32(&active, -1)
			release()
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&peak); got != 1 {
		t.Fatalf("peak concurrency: got %d, want 1", got)
	}
}

func TestMLXSchedulerBatchMode(t *testing.T) {
	baseURL := "http://127.0.0.1:25002/v1"
	ConfigureMLXScheduler(baseURL, 2, 10*time.Millisecond)

	var active int32
	var peak int32
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, _, _, err := AcquireMLXRequest(context.Background(), baseURL)
			if err != nil {
				t.Errorf("AcquireMLXRequest: %v", err)
				return
			}
			cur := atomic.AddInt32(&active, 1)
			if cur > atomic.LoadInt32(&peak) {
				atomic.StoreInt32(&peak, cur)
			}
			time.Sleep(25 * time.Millisecond)
			atomic.AddInt32(&active, -1)
			release()
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&peak); got < 2 {
		t.Fatalf("peak concurrency: got %d, want at least 2", got)
	}

	stats := MLXSchedulerSnapshot(baseURL)
	if stats.MaxConcurrency != 2 {
		t.Fatalf("max concurrency: got %d, want 2", stats.MaxConcurrency)
	}
	if stats.TotalBatches == 0 {
		t.Fatal("expected batch stats to be recorded")
	}
}
