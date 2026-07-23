package session

import (
	"context"
	"log"
	"net"
	"sync"
)

type Session struct {
	ID            uint32
	Conn          net.Conn
	Username      string
	Authenticated bool
	CWD           string
	DataConn      *net.UDPConn
	TransferType  byte
	Ctx           context.Context
	Cancel        context.CancelFunc
}

type Manager struct {
	mu       sync.RWMutex
	registry map[uint32]*Session
	nextID   uint32
}

func NewManager() *Manager {
	return &Manager{
		registry: make(map[uint32]*Session),
		nextID:   1,
	}
}

func (m *Manager) Register(conn net.Conn) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := m.nextID
	m.nextID++

	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{
		ID:           id,
		Conn:         conn,
		CWD:          "/",
		TransferType: 'A',
		Ctx:          ctx,
		Cancel:       cancel,
	}
	m.registry[id] = s

	log.Printf("[SESS] registered session %d", id)
	return s
}

func (m *Manager) Unregister(id uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if s, ok := m.registry[id]; ok {
		s.Cancel()
		if s.DataConn != nil {
			s.DataConn.Close()
		}
		delete(m.registry, id)
		log.Printf("[SESS] unregistered session %d", id)
	}
}

func (m *Manager) Get(id uint32) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.registry[id]
	return s, ok
}
