package session

import (
	"crypto/rand"
	"fmt"
	"sync"
	"time"
)

type Session struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
	}
}

func (sm *Manager) Create(name string) *Session {
	id := fmt.Sprintf("%s-%s", name, generateID()[:6])
	s := &Session{
		ID:        id,
		Name:      name,
		CreatedAt: time.Now(),
	}
	sm.mu.Lock()
	sm.sessions[id] = s
	sm.mu.Unlock()
	return s
}

func (sm *Manager) Get(id string) (*Session, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	s, ok := sm.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session %s not found", id)
	}
	return s, nil
}

func (sm *Manager) List() []Session {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	res := make([]Session, 0, len(sm.sessions))
	for _, s := range sm.sessions {
		res = append(res, *s)
	}
	return res
}

func (sm *Manager) Remove(id string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.sessions, id)
}

func (sm *Manager) GC() []string {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	var removed []string
	for id, s := range sm.sessions {
		if time.Since(s.CreatedAt) > 24*time.Hour {
			delete(sm.sessions, id)
			removed = append(removed, id)
		}
	}
	return removed
}

func generateID() []byte {
	b := make([]byte, 6)
	rand.Read(b)
	return b
}
