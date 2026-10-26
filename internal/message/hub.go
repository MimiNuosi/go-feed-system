package message

import "sync"

type Hub struct {
	mu   sync.RWMutex
	subs map[uint64]map[chan Item]struct{}
}

func NewHub() *Hub {
	return &Hub{subs: map[uint64]map[chan Item]struct{}{}}
}

func (h *Hub) Subscribe(userID uint64) (<-chan Item, func()) {
	ch := make(chan Item, 32)
	h.mu.Lock()
	if h.subs[userID] == nil {
		h.subs[userID] = map[chan Item]struct{}{}
	}
	h.subs[userID][ch] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			if set, ok := h.subs[userID]; ok {
				delete(set, ch)
				if len(set) == 0 {
					delete(h.subs, userID)
				}
			}
			h.mu.Unlock()
			close(ch)
		})
	}
}

func (h *Hub) Broadcast(item Item) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs[item.ReceiverID] {
		select {
		case ch <- item:
		default:
		}
	}
}
