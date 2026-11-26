package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// SSEEventType defines supported real-time event names pushed to a room's wall and phones.
type SSEEventType string

const (
	EventPhotoAdded     SSEEventType = "photo_added"
	EventPhotoDeleted   SSEEventType = "photo_deleted"
	EventPhotoSpotlight SSEEventType = "photo_spotlight"
	EventReaction       SSEEventType = "reaction"
	EventConfigUpdated  SSEEventType = "config_updated"
	// EventRoomDeleted tells open pages that the host deleted this event.
	EventRoomDeleted SSEEventType = "room_deleted"
)

// SSEMessage represents a typed payload broadcast to connected SSE clients.
type SSEMessage struct {
	Event SSEEventType `json:"event"`
	Data  any          `json:"data"`
}

// SSEHub fans out real-time updates. Clients subscribe to one room; broadcasts only reach that room.
type SSEHub struct {
	mu    sync.RWMutex
	rooms map[string]map[chan []byte]struct{}
}

// NewSSEHub creates a new SSE broadcast hub.
func NewSSEHub() *SSEHub {
	return &SSEHub{rooms: make(map[string]map[chan []byte]struct{})}
}

// Subscribe registers a client channel for a room and returns a cleanup function.
func (h *SSEHub) Subscribe(room string) (chan []byte, func()) {
	ch := make(chan []byte, 32)
	h.mu.Lock()
	if h.rooms[room] == nil {
		h.rooms[room] = make(map[chan []byte]struct{})
	}
	h.rooms[room][ch] = struct{}{}
	h.mu.Unlock()

	unsubscribe := func() {
		h.mu.Lock()
		if clients, ok := h.rooms[room]; ok {
			if _, ok := clients[ch]; ok {
				delete(clients, ch)
				close(ch)
			}
			if len(clients) == 0 {
				delete(h.rooms, room)
			}
		}
		h.mu.Unlock()
	}
	return ch, unsubscribe
}

// Broadcast serializes the payload as JSON and sends an SSE frame to every client in the room.
func (h *SSEHub) Broadcast(room string, event SSEEventType, payload any) {
	rawJSON, err := json.Marshal(SSEMessage{Event: event, Data: payload})
	if err != nil {
		return
	}
	frame := []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event, string(rawJSON)))

	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.rooms[room] {
		select {
		case ch <- frame:
		default:
			// Drop if client buffer is full to avoid blocking the broadcaster
		}
	}
}

// ClientCount returns the number of listeners in a room.
func (h *SSEHub) ClientCount(room string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.rooms[room])
}

// ServeRoom streams Server-Sent Events for one room until the client disconnects.
func (h *SSEHub) ServeRoom(w http.ResponseWriter, r *http.Request, room string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, unsubscribe := h.Subscribe(room)
	defer unsubscribe()

	// Send initial connected comment so clients resolve onopen immediately
	_, _ = fmt.Fprintf(w, ": connected %d\n\n", time.Now().Unix())
	flusher.Flush()

	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if _, err := w.Write(msg); err != nil {
				return
			}
			flusher.Flush()
		case t := <-keepalive.C:
			if _, err := fmt.Fprintf(w, ": keepalive %d\n\n", t.Unix()); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
