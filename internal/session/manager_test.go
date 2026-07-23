package session

import (
	"net"
	"sync"
	"testing"
)

type fakeConn struct{ net.Conn }

func TestNewManager(t *testing.T) {
	m := NewManager()
	if m == nil {
		t.Fatal("NewManager() should not return nil")
	}
}

func TestRegister(t *testing.T) {
	m := NewManager()
	conn1, _ := net.Pipe()
	defer conn1.Close()

	s := m.Register(conn1)
	s2 := m.Register(conn1)

	if s.ID == s2.ID {
		t.Error("register should assign unique IDs")
	}
	if s.ID == 0 || s2.ID == 0 {
		t.Error("register should assign non-zero IDs")
	}
}

func TestGet(t *testing.T) {
	m := NewManager()
	conn, _ := net.Pipe()
	defer conn.Close()

	s := m.Register(conn)
	got, ok := m.Get(s.ID)
	if !ok {
		t.Fatal("Get should find registered session")
	}
	if got.ID != s.ID {
		t.Errorf("Get returned session ID %d, want %d", got.ID, s.ID)
	}
}

func TestGet_Missing(t *testing.T) {
	m := NewManager()
	_, ok := m.Get(9999)
	if ok {
		t.Error("Get for unregistered ID should return false")
	}
}

func TestUnregister(t *testing.T) {
	m := NewManager()
	conn, _ := net.Pipe()
	defer conn.Close()

	s := m.Register(conn)
	m.Unregister(s.ID)

	_, ok := m.Get(s.ID)
	if ok {
		t.Error("Get should return false after Unregister")
	}
}

func TestConcurrentAccess(t *testing.T) {
	m := NewManager()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			conn, _ := net.Pipe()
			defer conn.Close()
			s := m.Register(conn)
			got, ok := m.Get(s.ID)
			if !ok || got.ID != s.ID {
				t.Errorf("concurrent access failed for iteration %d", n)
			}
			m.Unregister(s.ID)
		}(i)
	}
	wg.Wait()
}
