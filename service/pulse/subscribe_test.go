package pulse

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"testing"
)

func goroutinesIn(frame string) int {
	buf := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			count := 0
			for stack := range bytes.SplitSeq(buf[:n], []byte("\n\n")) {
				if bytes.Contains(stack, []byte(frame)) {
					count++
				}
			}
			return count
		}
		buf = make([]byte, 2*len(buf))
	}
}

func subscriptionGoroutines() int {
	return goroutinesIn("service/pulse.(*Service).subscribeLocked.func1(")
}

func captureGoroutines() int {
	return goroutinesIn("service/pulse.(*Capture).run(")
}

func TestNoSubscriptionGoroutineOutlivesStopOrClose(t *testing.T) {
	s := connect(t, fixture(t))
	subscribe := func() func() {
		t.Helper()
		_, stop, err := s.Subscribe(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return stop
	}
	if n := subscriptionGoroutines(); n != 0 {
		t.Fatalf("%d subscription goroutines before any subscription", n)
	}

	stop := subscribe()
	waitFor(t, "one subscription goroutine", func() bool { return subscriptionGoroutines() == 1 })
	stop()
	if n := subscriptionGoroutines(); n != 0 {
		t.Errorf("%d subscription goroutines left once stop returned", n)
	}

	subscribe()
	subscribe()
	waitFor(t, "two subscription goroutines", func() bool { return subscriptionGoroutines() == 2 })
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if n := subscriptionGoroutines(); n != 0 {
		t.Errorf("%d subscription goroutines left once Close returned", n)
	}
}

func TestNoCaptureGoroutineOutlivesClose(t *testing.T) {
	s := connect(t, fixture(t))
	for range 2 {
		if _, err := s.Capture(CaptureDefaultMonitor(), cavaSpec(1), func([]byte) {}); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "two capture and two subscription goroutines", func() bool {
		return captureGoroutines() == 2 && subscriptionGoroutines() == 2
	})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if n := captureGoroutines(); n != 0 {
		t.Errorf("%d capture goroutines left once Close returned", n)
	}
	if n := subscriptionGoroutines(); n != 0 {
		t.Errorf("%d subscription goroutines left once Close returned", n)
	}
	if _, err := s.Capture(CaptureDefaultMonitor(), cavaSpec(1), func([]byte) {}); !errors.Is(err, ErrClosed) {
		t.Errorf("Capture after Close: %v, want ErrClosed", err)
	}
}
