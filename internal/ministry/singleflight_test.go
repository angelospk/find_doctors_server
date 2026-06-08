package ministry

import (
	"errors"
	"testing"
	"time"
)

// A panic inside fn() must not leave the key wedged; the next call for the same
// key should proceed normally instead of deadlocking on the WaitGroup.
func TestSingleflight_PanicDoesNotDeadlock(t *testing.T) {
	sf := newSingleflight()

	func() {
		defer func() { _ = recover() }()
		_, _ = sf.Do("k", func() (any, error) {
			panic("boom")
		})
	}()

	done := make(chan struct{})
	go func() {
		_, _ = sf.Do("k", func() (any, error) { return 42, nil })
		close(done)
	}()

	select {
	case <-done:
		// recovered cleanly
	case <-time.After(2 * time.Second):
		t.Fatal("singleflight deadlocked after panic in fn()")
	}
}

func TestSingleflight_ReturnsValueAndError(t *testing.T) {
	sf := newSingleflight()
	sentinel := errors.New("x")
	v, err := sf.Do("k", func() (any, error) { return 7, sentinel })
	if v != 7 || !errors.Is(err, sentinel) {
		t.Fatalf("got (%v, %v)", v, err)
	}
}
