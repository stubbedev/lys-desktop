package pulse

import (
	"bytes"
	"context"
	"runtime"
	"testing"
)

func subscriptionGoroutines() int {
	buf := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return bytes.Count(buf[:n], []byte("service/pulse.(*Service).Subscribe.func"))
		}
		buf = make([]byte, 2*len(buf))
	}
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
	if n := subscriptionGoroutines(); n != 1 {
		t.Fatalf("%d subscription goroutines for one subscription, want 1", n)
	}
	stop()
	if n := subscriptionGoroutines(); n != 0 {
		t.Errorf("%d subscription goroutines left once stop returned", n)
	}

	subscribe()
	subscribe()
	if n := subscriptionGoroutines(); n != 2 {
		t.Fatalf("%d subscription goroutines for two subscriptions, want 2", n)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if n := subscriptionGoroutines(); n != 0 {
		t.Errorf("%d subscription goroutines left once Close returned", n)
	}
}
