package alerts

import "sync"

// hub fans values out to subscribers without ever blocking the publisher. A
// subscriber that falls more than size values behind loses the oldest ones.
type hub[T any] struct {
	size int

	mu     sync.Mutex
	subs   map[chan T]struct{}
	closed bool
}

func newHub[T any](size int) *hub[T] {
	return &hub[T]{size: size, subs: map[chan T]struct{}{}}
}

// subscribe returns a channel of published values and a function that
// unsubscribes and closes it. The channel is also closed by close.
func (h *hub[T]) subscribe() (<-chan T, func()) {
	ch := make(chan T, h.size)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		close(ch)
		return ch, func() {}
	}
	h.subs[ch] = struct{}{}

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if _, ok := h.subs[ch]; ok {
				delete(h.subs, ch)
				close(ch)
			}
		})
	}
}

// publish sends v to every subscriber and returns how many older values it
// had to drop to make room.
func (h *hub[T]) publish(v T) (dropped int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		for {
			select {
			case ch <- v:
			default:
				// Full: drop the oldest unread value and try again. The
				// subscriber may have read it meanwhile, which is fine too.
				select {
				case <-ch:
					dropped++
				default:
				}
				continue
			}
			break
		}
	}
	return dropped
}

// close closes every subscriber channel. Later subscribers get a closed
// channel straight away.
func (h *hub[T]) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		delete(h.subs, ch)
		close(ch)
	}
	h.closed = true
}
