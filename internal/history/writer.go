package history

import (
	"context"
	"log/slog"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

const (
	// maxPending caps how many samples the writer holds while writes fail,
	// so a broken database cannot eat the Pi's memory. The oldest go first.
	maxPending = 1000
	// finalFlushTimeout bounds the last write on shutdown.
	finalFlushTimeout = 10 * time.Second
)

// Writer stores the series of every snapshot it receives. Snapshots are
// collected in memory and written in one transaction at most once per
// interval, measured on the snapshot timestamps. The first snapshot is
// written right away.
type Writer struct {
	store    *Store
	interval time.Duration
	logger   *slog.Logger

	pending   []Sample
	lastFlush time.Time // timestamp of the snapshot that triggered the last write
	failing   bool
}

// NewWriter creates a writer that stores into store every interval. A nil
// logger means slog.Default().
func NewWriter(store *Store, interval time.Duration, logger *slog.Logger) *Writer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Writer{store: store, interval: interval, logger: logger}
}

// Run stores the snapshots from snaps, usually a metrics.Sampler
// subscription, until snaps is closed or ctx is done. Before returning it
// writes whatever is still pending and returns the error of that last write.
func (w *Writer) Run(ctx context.Context, snaps <-chan metrics.Snapshot) error {
	for {
		select {
		case snap, ok := <-snaps:
			if !ok {
				return w.final(ctx)
			}
			w.add(ctx, snap)
		case <-ctx.Done():
			// Keep what the sampler already handed us.
			for {
				select {
				case snap, ok := <-snaps:
					if ok {
						w.queue(snap)
						continue
					}
				default:
				}
				return w.final(ctx)
			}
		}
	}
}

// add queues snap and writes the pending samples once an interval has passed
// since the last write. A clock that jumps backwards also triggers a write,
// so samples never pile up waiting for time to catch up.
func (w *Writer) add(ctx context.Context, snap metrics.Snapshot) {
	w.queue(snap)
	since := snap.Timestamp.Sub(w.lastFlush)
	if !w.lastFlush.IsZero() && since >= 0 && since < w.interval {
		return
	}
	w.lastFlush = snap.Timestamp
	w.report(w.flush(ctx))
}

func (w *Writer) queue(snap metrics.Snapshot) {
	if len(snap.Series) == 0 {
		return
	}
	if len(w.pending) >= maxPending {
		w.pending = w.pending[1:]
	}
	w.pending = append(w.pending, Sample{Time: snap.Timestamp, Values: snap.Series})
}

// flush writes the pending samples. On failure they stay pending for the
// next attempt.
func (w *Writer) flush(ctx context.Context) error {
	if len(w.pending) == 0 {
		return nil
	}
	if err := w.store.Insert(ctx, w.pending); err != nil {
		return err
	}
	clear(w.pending) // drop references to the snapshot maps
	w.pending = w.pending[:0]
	return nil
}

// report logs a failed write once, and once more when writes work again.
func (w *Writer) report(err error) {
	switch {
	case err != nil && !w.failing:
		w.logger.Error("history write failed", "err", err, "pending", len(w.pending))
	case err == nil && w.failing:
		w.logger.Info("history write recovered")
	}
	w.failing = err != nil
}

// final writes what is pending on shutdown, even if ctx is already done. The
// caller logs its error.
func (w *Writer) final(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalFlushTimeout)
	defer cancel()
	return w.flush(ctx)
}
