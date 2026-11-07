package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/alerts"
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
// away, then one "snapshot" event per sample. With an alert source it also
// sends the current alerts as an "alerts" event on connect and again whenever
// they change. It returns when the client goes away, the server shuts down or
// the snapshot source stops.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	// Subscribe before reading Latest so no sample falls in between. A sample
	// that lands in both is skipped by the timestamp check below.
	snaps, unsubscribe := s.snapshot.Subscribe()
	defer unsubscribe()
	var alertSets <-chan alerts.Set // stays nil without an alert source
	if s.alerts != nil {
		var unsubscribeAlerts func()
		alertSets, unsubscribeAlerts = s.alerts.Subscribe()
		defer unsubscribeAlerts()
	}

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

	// The alert set has the same race as snapshots, and the same fix: a set
	// that was already sent is skipped by its version.
	var alertsVersion uint64
	alertsSent := false
	sendAlerts := func(set alerts.Set) bool {
		if alertsSent && set.Version <= alertsVersion {
			return true
		}
		buf.Reset()
		buf.WriteString("event: alerts\ndata: ")
		if err := json.NewEncoder(&buf).Encode(set); err != nil {
			s.logger.Warn("encode alerts for stream", "err", err)
			return true
		}
		buf.WriteByte('\n')
		if !write(buf.Bytes()) {
			return false
		}
		alertsVersion, alertsSent = set.Version, true
		return true
	}

	if snap, ok := s.snapshot.Latest(); ok {
		if !sendSnapshot(snap) {
			return
		}
	} else if !write(nil) { // send the headers now
		return
	}
	if s.alerts != nil && !sendAlerts(s.alerts.Alerts()) {
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
		case set, ok := <-alertSets:
			if !ok {
				// The alert source stopped; keep streaming snapshots.
				alertSets = nil
				continue
			}
			if !sendAlerts(set) {
				return
			}
		case <-heartbeat.C:
			if !write([]byte(": heartbeat\n\n")) {
				return
			}
		}
	}
}
