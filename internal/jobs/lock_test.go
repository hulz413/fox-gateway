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

func TestKeyedLockSerializesSameKey(t *testing.T) {
	lock := NewKeyedLock()
	lock.Acquire("chat_1")

	started := make(chan struct{})
	released := make(chan struct{})
	go func() {
		close(started)
		lock.Acquire("chat_1")
		close(released)
		lock.Release("chat_1")
	}()

	<-started
	select {
	case <-released:
		t.Fatal("same keyed lock should block")
	case <-time.After(50 * time.Millisecond):
	}

	lock.Release("chat_1")
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("same keyed lock did not proceed after release")
	}
}

func TestKeyedLockAllowsDifferentKeys(t *testing.T) {
	lock := NewKeyedLock()
	lock.Acquire("chat_1")

	released := make(chan struct{})
	go func() {
		lock.Acquire("chat_2")
		close(released)
		lock.Release("chat_2")
	}()

	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("different keys should not block each other")
	}

	lock.Release("chat_1")
}
