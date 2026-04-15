package jobs

import (
	"sync"
	"testing"
	"time"
)

func TestWorkspaceLockSerializesMutations(t *testing.T) {
	lock := NewWorkspaceLock()
	lock.Acquire("first")

	started := make(chan struct{})
	released := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		close(started)
		lock.Acquire("second")
		close(released)
		lock.Release("second")
	}()

	<-started
	select {
	case <-released:
		t.Fatal("second acquire should block while first lock is held")
	case <-time.After(50 * time.Millisecond):
	}

	lock.Release("first")
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("second acquire did not proceed after release")
	}
	wg.Wait()
}
