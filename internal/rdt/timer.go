package rdt

import (
	"sync"
	"time"
)

const (
	initialTimeout = 500 * time.Millisecond
	maxTimeout     = 60 * time.Second
	maxRetransmits = 3
)

type TimerManager struct {
	mu       sync.Mutex
	timers   map[uint32]*time.Timer
	backoff  map[uint32]int
	timeout  time.Duration
	onExpire func(seq uint32)
}

func NewTimerManager(baseTimeout time.Duration, callback func(seq uint32)) *TimerManager {
	if baseTimeout <= 0 {
		baseTimeout = initialTimeout
	}
	return &TimerManager{
		timers:   make(map[uint32]*time.Timer),
		backoff:  make(map[uint32]int),
		timeout:  baseTimeout,
		onExpire: callback,
	}
}

func (tm *TimerManager) Start(seq uint32) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if t, ok := tm.timers[seq]; ok {
		if !t.Stop() {
			select {
			case <-t.C:
			default:
			}
		}
	}

	retries := tm.backoff[seq]
	delay := tm.timeout
	for i := 0; i < retries; i++ {
		delay *= 2
	}
	if delay > maxTimeout {
		delay = maxTimeout
	}

	tm.timers[seq] = time.AfterFunc(delay, func() {
		tm.mu.Lock()
		tm.backoff[seq]++
		cb := tm.onExpire
		tm.mu.Unlock()
		cb(seq)
	})
}

func (tm *TimerManager) Cancel(seq uint32) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if t, ok := tm.timers[seq]; ok {
		t.Stop()
		delete(tm.timers, seq)
	}
	delete(tm.backoff, seq)
}

func (tm *TimerManager) StopAll() {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	for _, t := range tm.timers {
		t.Stop()
	}
	tm.timers = make(map[uint32]*time.Timer)
	tm.backoff = make(map[uint32]int)
}
