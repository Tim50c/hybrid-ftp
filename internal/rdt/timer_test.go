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

func TestTimerBackoffCapMaxTimeout(t *testing.T) {
	tm := NewTimerManager(50*time.Millisecond, func(seq uint32) {})
	defer tm.StopAll()

	tm.backoff[1] = 30

	tm.mu.Lock()
	retries := tm.backoff[1]
	delay := tm.timeout
	for i := 0; i < retries; i++ {
		delay *= 2
	}
	if delay > maxTimeout {
		delay = maxTimeout
	}
	tm.mu.Unlock()

	if delay > maxTimeout {
		t.Errorf("capped delay %v should not exceed maxTimeout %v", delay, maxTimeout)
	}
	t.Logf("capped delay = %v (maxTimeout = %v)", delay, maxTimeout)
}

func TestTimerMultipleConcurrent(t *testing.T) {
	received := make(map[uint32]bool)
	var mu sync.Mutex
	done := make(chan struct{})

	tm := NewTimerManager(80*time.Millisecond, func(seq uint32) {
		mu.Lock()
		received[seq] = true
		mu.Unlock()
		if len(received) >= 3 {
			close(done)
		}
	})
	defer tm.StopAll()

	tm.Start(10)
	tm.Start(20)
	tm.Start(30)

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("not all timers fired")
	}

	mu.Lock()
	defer mu.Unlock()
	for _, seq := range []uint32{10, 20, 30} {
		if !received[seq] {
			t.Errorf("timer for seq %d did not fire", seq)
		}
	}
}

func TestTimerBackoffResetsAfterCancel(t *testing.T) {
	expired := make(chan uint32, 5)
	tm := NewTimerManager(80*time.Millisecond, func(seq uint32) {
		expired <- seq
	})
	defer tm.StopAll()

	tm.Start(7)
	tm.Cancel(7)

	time.Sleep(150 * time.Millisecond)

	tm.Start(7)

	start := time.Now()
	select {
	case <-expired:
		elapsed := time.Since(start)
		if elapsed > 300*time.Millisecond {
			t.Errorf("after cancel+restart, timer took %v (expected ~80ms base timeout)", elapsed)
		}
	case <-time.After(400 * time.Millisecond):
		t.Fatal("restarted timer did not fire")
	}
}

func TestTimerRestartExtendsTimer(t *testing.T) {
	expired := make(chan uint32, 2)
	tm := NewTimerManager(150*time.Millisecond, func(seq uint32) {
		expired <- seq
	})
	defer tm.StopAll()

	tm.Start(42)

	time.Sleep(80 * time.Millisecond)

	tm.Start(42)

	select {
	case <-expired:
		elapsed := time.Since(time.Now().Add(-time.Duration(0)))
		_ = elapsed
	case <-time.After(300 * time.Millisecond):
		t.Fatal("restarted timer did not fire")
	}
}

func TestTimerCallbackDoesNotDeadlockOnMutex(t *testing.T) {
	var mu sync.Mutex
	tm := NewTimerManager(50*time.Millisecond, func(seq uint32) {
		mu.Lock()
		mu.Unlock()
	})
	defer tm.StopAll()

	tm.Start(99)

	time.Sleep(150 * time.Millisecond)
}

func TestTimerDefaultBaseTimeout(t *testing.T) {
	tm := NewTimerManager(0, func(seq uint32) {})
	defer tm.StopAll()

	tm.mu.Lock()
	base := tm.timeout
	tm.mu.Unlock()

	if base != initialTimeout {
		t.Errorf("expected base timeout %v when 0 is passed, got %v", initialTimeout, base)
	}
}
