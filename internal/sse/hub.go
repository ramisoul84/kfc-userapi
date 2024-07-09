package sse

import (
	"sync"

	"github.com/ramisoul84/kfc-userapi/pkg/logger"
)

// Subscriber is a channel that receives event payloads. The SSE handler
// owns it and closes it when the client disconnects.
type Subscriber chan []byte

// Hub is an in-memory pub/sub keyed by restaurant ID.
//
// Two kinds of subscribers are supported: those subscribed to a specific
// restaurant, and those subscribed globally (empty key), which receive
// every event. A global event broadcasts to all subscribers regardless
// of their key.
type Hub struct {
	mu sync.RWMutex

	// byRestaurant maps a restaurant ID to its subscribers.
	byRestaurant map[string]map[Subscriber]struct{}

	// global holds subscribers that want every event.
	global map[Subscriber]struct{}

	logger *logger.Logger
}

func NewHub(log *logger.Logger) *Hub {
	return &Hub{
		byRestaurant: make(map[string]map[Subscriber]struct{}),
		global:       make(map[Subscriber]struct{}),
		logger:       log,
	}
}

// Subscribe registers a subscriber.
//
// If restaurantID is empty, the subscriber receives every event.
// Otherwise it receives only events for that restaurant (and global events).
func (h *Hub) Subscribe(restaurantID string) Subscriber {
	// Small buffer so a slow writer doesn't block the broadcaster.
	// If the buffer fills, we drop the event and the client
	// re-syncs on its next HTTP fetch.
	ch := make(Subscriber, 8)

	h.mu.Lock()
	defer h.mu.Unlock()

	if restaurantID == "" {
		h.global[ch] = struct{}{}
	} else {
		if h.byRestaurant[restaurantID] == nil {
			h.byRestaurant[restaurantID] = make(map[Subscriber]struct{})
		}
		h.byRestaurant[restaurantID][ch] = struct{}{}
	}

	h.logger.Debug("sse subscriber added",
		"restaurant_id", restaurantID,
		"total_restaurant_subs", len(h.byRestaurant[restaurantID]),
		"total_global_subs", len(h.global),
	)

	return ch
}

// Unsubscribe removes a subscriber and closes its channel.
func (h *Hub) Unsubscribe(restaurantID string, ch Subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if restaurantID == "" {
		delete(h.global, ch)
	} else if subs, ok := h.byRestaurant[restaurantID]; ok {
		delete(subs, ch)
		if len(subs) == 0 {
			delete(h.byRestaurant, restaurantID)
		}
	}

	close(ch)
}

// Publish sends an event to every matching subscriber.
//
// restaurantID == "" broadcasts to all subscribers (global event).
// Otherwise it goes to subscribers of that restaurant plus all global
// subscribers.
//
// Non-blocking: if a subscriber's buffer is full, the event is dropped
// for that subscriber. The kiosk re-fetches the menu on any event it
// receives, so a missed event just means a stale display until the next
// event or TTL expiry.
func (h *Hub) Publish(restaurantID string, payload []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	delivered := 0

	if restaurantID == "" {
		// Global event → everyone.
		for ch := range h.global {
			if trySend(ch, payload) {
				delivered++
			}
		}
		for _, subs := range h.byRestaurant {
			for ch := range subs {
				if trySend(ch, payload) {
					delivered++
				}
			}
		}
	} else {
		// Specific restaurant → its subscribers + global subscribers.
		if subs, ok := h.byRestaurant[restaurantID]; ok {
			for ch := range subs {
				if trySend(ch, payload) {
					delivered++
				}
			}
		}
		for ch := range h.global {
			if trySend(ch, payload) {
				delivered++
			}
		}
	}

	h.logger.Debug("sse event published",
		"restaurant_id", restaurantID,
		"delivered", delivered,
	)
}

func trySend(ch Subscriber, payload []byte) bool {
	select {
	case ch <- payload:
		return true
	default:
		return false // buffer full, drop
	}
}
