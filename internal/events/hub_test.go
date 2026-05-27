package events

import (
	"sync"
	"testing"
	"time"
)

func TestHubSubscribePublish(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe()
	defer cancel()

	h.Publish("test.event", map[string]string{"key": "value"})

	select {
	case ev := <-ch:
		if ev.Type != "test.event" {
			t.Fatalf("expected type test.event, got %s", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestHubMultiSubscriber(t *testing.T) {
	h := NewHub()
	ch1, cancel1 := h.Subscribe()
	defer cancel1()
	ch2, cancel2 := h.Subscribe()
	defer cancel2()

	h.Publish("multi", "data")

	for _, ch := range []<-chan Event{ch1, ch2} {
		select {
		case ev := <-ch:
			if ev.Type != "multi" {
				t.Fatalf("expected multi, got %s", ev.Type)
			}
		case <-time.After(time.Second):
			t.Fatal("timeout")
		}
	}
}

func TestHubUnsubscribe(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe()
	cancel()

	_, ok := <-ch
	if ok {
		t.Fatal("channel should be closed after unsubscribe")
	}
}

func TestHubBufferDrop(t *testing.T) {
	h := NewHub()
	_, cancel := h.Subscribe()
	defer cancel()

	for i := 0; i < 100; i++ {
		h.Publish("flood", i)
	}
}

func TestHubPublishNoSubscribers(t *testing.T) {
	h := NewHub()
	h.Publish("no-one", "listening")
}

func TestHubConcurrentPubSub(t *testing.T) {
	h := NewHub()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, cancel := h.Subscribe()
			defer cancel()
			h.Publish("concurrent", "data")
			select {
			case <-ch:
			case <-time.After(100 * time.Millisecond):
			}
		}()
	}
	wg.Wait()
}
