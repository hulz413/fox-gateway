package jobs

import "sync"

type WorkspaceLock struct {
	mu      sync.Mutex
	cond    *sync.Cond
	locked  bool
	ownerID string
}

func NewWorkspaceLock() *WorkspaceLock {
	l := &WorkspaceLock{}
	l.cond = sync.NewCond(&l.mu)
	return l
}

func (l *WorkspaceLock) Acquire(ownerID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for l.locked {
		l.cond.Wait()
	}
	l.locked = true
	l.ownerID = ownerID
}

func (l *WorkspaceLock) Release(ownerID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.locked {
		return
	}
	if l.ownerID != "" && ownerID != "" && l.ownerID != ownerID {
		return
	}
	l.locked = false
	l.ownerID = ""
	l.cond.Broadcast()
}
