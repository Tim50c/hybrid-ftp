package rdt

import (
	"sync"
	"testing"
	"time"
)

func TestTimerStartAndCancel(t *testing.T) {
	expired := make(chan uint32, 10)
	tm := NewTimerManager(200*time.Millisecond, func(seq uint32) {
		expired <- seq
	})
	defer tm.StopAll()

	tm.Start(1)
	tm.Start(2)
	tm.Cancel(1)

	select {
	case seq := <-expired:
		if seq != 2 {
			t.Errorf("expected seq 2 to expire, got %d", seq)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("timer for seq 2 should have expired")
	}

	select {
	case <-expired:
		t.Error("timer for seq 1 should have been cancelled")
	case <-time.After(300 * time.Millisecond):
		// expected: no expiry for seq 1
	}
}

func TestTimerBackoff(t *testing.T) {
	var mu sync.Mutex
	expiries := make([]uint32, 0)
	tm := NewTimerManager(100*time.Millisecond, func(seq uint32) {
		mu.Lock()
		expiries = append(expiries, seq)
		mu.Unlock()
	})
	defer tm.StopAll()

	tm.Start(5)
	time.Sleep(200 * time.Millisecond)

	tm.Start(5)
	time.Sleep(250 * time.Millisecond)

	tm.Start(5)
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	count := len(expiries)
	mu.Unlock()
	t.Logf("timer for seq 5 expired %d times", count)
	if count < 2 {
		t.Errorf("expected at least 2 expiries, got %d", count)
	}
}

func TestTimerStopAll(t *testing.T) {
	expired := make(chan uint32, 10)
	tm := NewTimerManager(100*time.Millisecond, func(seq uint32) {
		expired <- seq
	})

	tm.Start(1)
	tm.Start(2)
	tm.Start(3)
	tm.StopAll()

	select {
	case seq := <-expired:
		t.Errorf("no timer should expire after StopAll, got seq %d", seq)
	case <-time.After(200 * time.Millisecond):
		// expected
	}
}
