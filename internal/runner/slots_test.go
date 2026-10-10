package runner

import (
	"context"
	"testing"
	"time"
)

func TestSlotsFollowTheLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s := newSlots(2)
	if !s.acquire(ctx) || !s.acquire(ctx) {
		t.Fatal("two slots")
	}
	got := make(chan bool, 1)
	go func() { got <- s.acquire(ctx) }()
	select {
	case <-got:
		t.Fatal("a third slot at limit 2")
	case <-time.After(50 * time.Millisecond):
	}
	s.setLimit(3) // raised from Forge: the waiting claim goes ahead
	if !<-got {
		t.Fatal("no slot after raising the limit")
	}
	s.setLimit(1) // lowered: running ones finish, nothing new until below it
	s.release()
	s.release()
	short, stop := context.WithTimeout(ctx, 50*time.Millisecond)
	defer stop()
	if s.acquire(short) {
		t.Fatal("a slot while still at the lowered limit")
	}
	s.release()
	if !s.acquire(ctx) {
		t.Fatal("no slot once below the limit")
	}
	s.release()
	s.drain()
}

func TestMaxConcurrent(t *testing.T) {
	c := &Config{}
	if c.maxConcurrent(0) != DefaultMaxConcurrent || c.maxConcurrent(6) != 6 {
		t.Fatalf("no local cap: %d %d", c.maxConcurrent(0), c.maxConcurrent(6))
	}
	c.MaxConcurrent = 3
	if c.maxConcurrent(6) != 3 || c.maxConcurrent(2) != 2 {
		t.Fatalf("local cap 3: %d %d", c.maxConcurrent(6), c.maxConcurrent(2))
	}
}
