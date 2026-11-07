package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

const (
	// heartbeatInterval is how often a stream gets an SSE comment so proxies
	// keep the connection open even when no snapshots are flowing.
	heartbeatInterval = 15 * time.Second
	// streamWriteTimeout bounds each write to a stream. It replaces the
	// server's write timeout for the connection and drops clients that stop
	// reading.
	streamWriteTimeout = 10 * time.Second
)

// handleStream sends snapshots as Server-Sent Events: the latest one right
// away, then one "snapshot" event per sample. It returns when the client goes
// away, the server shuts down or the snapshot source stops.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	// Subscribe before reading Latest so no sample falls in between. A sample
	// that lands in both is skipped by the timestamp check below.
	snaps, unsubscribe := s.snapshot.Subscribe()
	defer unsubscribe()

	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no") // stop nginx from buffering the stream
	w.WriteHeader(http.StatusOK)

	var buf bytes.Buffer
	write := func(msg []byte) bool {
		// Push the deadline forward before every write so the server's
		// WriteTimeout does not cut the stream, while a client that stops
		// reading still gets dropped.
		err := rc.SetWriteDeadline(time.Now().Add(s.streamWriteTimeout))
		if err != nil && !errors.Is(err, http.ErrNotSupported) {
			return false
		}
		if _, err := w.Write(msg); err != nil {
			return false
		}
		return rc.Flush() == nil
	}

	var last time.Time
	sendSnapshot := func(snap metrics.Snapshot) bool {
		if !last.IsZero() && !snap.Timestamp.After(last) {
			return true
		}
		buf.Reset()
		buf.WriteString("event: snapshot\ndata: ")
		if err := json.NewEncoder(&buf).Encode(snap); err != nil {
			s.logger.Warn("encode snapshot for stream", "err", err)
			return true
		}
		// Encode ends with a newline; one more ends the event.
		buf.WriteByte('\n')
		if !write(buf.Bytes()) {
			return false
		}
		last = snap.Timestamp
		return true
	}

	if snap, ok := s.snapshot.Latest(); ok {
		if !sendSnapshot(snap) {
			return
		}
	} else if !write(nil) { // send the headers now
		return
	}

	heartbeat := time.NewTicker(s.heartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case snap, ok := <-snaps:
			if !ok {
				return
			}
			if !sendSnapshot(snap) {
				return
			}
		case <-heartbeat.C:
			if !write([]byte(": heartbeat\n\n")) {
				return
			}
		}
	}
}
