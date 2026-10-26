package notification

import "sync"

// Hub 是单实例内的 SSE 订阅表。
//
// 多实例部署需要换成 Redis Pub/Sub（或 RabbitMQ fanout），
// 这里先把单实例的推送链路做通。
type Hub struct {
	mu   sync.RWMutex
	subs map[uint64]map[chan Item]struct{}
}

func NewHub() *Hub {
	return &Hub{subs: map[uint64]map[chan Item]struct{}{}}
}

func (h *Hub) Subscribe(userID uint64) (<-chan Item, func()) {
	ch := make(chan Item, 16)
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

// Broadcast 非阻塞投递：订阅者处理慢时丢事件，但不会拖住写入方。
func (h *Hub) Broadcast(item Item) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs[item.UserID] {
		select {
		case ch <- item:
		default:
		}
	}
}
