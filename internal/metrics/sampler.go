package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Snapshot is the result of one sampling round.
//
// Snapshots are shared between the sampler, HTTP handlers and subscribers, so
// treat them as read-only.
type Snapshot struct {
	Timestamp time.Time `json:"timestamp"`
	// Collectors maps each collector name to its section, already encoded as
	// a json.RawMessage. A failed collector's section is {"error": "..."}.
	Collectors map[string]any `json:"collectors"`
	// Series merges the series of every collector that succeeded.
	Series map[string]float64 `json:"series"`
}

// SubscriberBuffer is how many snapshots a subscriber can fall behind before
// the oldest unread one is dropped.
const SubscriberBuffer = 4

// Options configures a Sampler. Only Interval is required.
type Options struct {
	// Interval between sampling rounds.
	Interval time.Duration
	// Timeout for each collector in a round. Defaults to Interval.
	Timeout time.Duration
	// Collectors to run. Defaults to Registered().
	Collectors []Collector
	// FS defaults to os.DirFS("/").
	FS fs.FS
	// Runner defaults to ExecRunner.
	Runner CommandRunner
	// Logger defaults to slog.Default().
	Logger *slog.Logger
	// Now defaults to time.Now.
	Now func() time.Time
}

// Sampler runs the collectors on an interval and keeps the latest snapshot.
type Sampler struct {
	interval time.Duration
	timeout  time.Duration
	fs       fs.FS
	runner   CommandRunner
	logger   *slog.Logger
	now      func() time.Time
	states   []*collectorState

	sampleMu sync.Mutex // serialises Sample calls

	latestMu sync.RWMutex
	latest   Snapshot
	hasValue bool

	subMu  sync.Mutex
	subs   map[chan Snapshot]struct{}
	closed bool
}

type collectorState struct {
	c Collector
	// busy is set while a Collect call is running, which can outlive the
	// round that started it if the collector ignores its context.
	busy atomic.Bool
	// prev and lastErr are only touched under Sampler.sampleMu.
	prev    time.Time
	lastErr string
}

type outcome struct {
	data   json.RawMessage
	series map[string]float64
	err    error
}

// NewSampler creates a sampler. Call Run to start sampling.
func NewSampler(opts Options) *Sampler {
	s := &Sampler{
		interval: opts.Interval,
		timeout:  opts.Timeout,
		fs:       opts.FS,
		runner:   opts.Runner,
		logger:   opts.Logger,
		now:      opts.Now,
		subs:     map[chan Snapshot]struct{}{},
	}
	if s.timeout <= 0 {
		s.timeout = s.interval
	}
	if s.fs == nil {
		s.fs = os.DirFS("/")
	}
	if s.runner == nil {
		s.runner = ExecRunner{}
	}
	if s.logger == nil {
		s.logger = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}
	collectors := opts.Collectors
	if collectors == nil {
		collectors = Registered()
	}
	for _, c := range collectors {
		s.states = append(s.states, &collectorState{c: c})
	}
	return s
}

// Run samples immediately and then every interval until ctx is done. It
// closes all subscriber channels before returning.
func (s *Sampler) Run(ctx context.Context) error {
	if s.interval <= 0 {
		return fmt.Errorf("metrics: invalid sample interval %v", s.interval)
	}
	defer s.closeSubscribers()

	names := make([]string, len(s.states))
	for i, st := range s.states {
		names[i] = st.c.Name()
	}
	s.logger.Info("sampler started", "interval", s.interval.String(), "collectors", names)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		s.Sample(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Sample runs one round: every collector in parallel, each bounded by the
// timeout. The new snapshot becomes Latest and is sent to subscribers.
func (s *Sampler) Sample(ctx context.Context) Snapshot {
	s.sampleMu.Lock()
	defer s.sampleMu.Unlock()

	now := s.now()
	outcomes := make([]outcome, len(s.states))
	var wg sync.WaitGroup
	for i, st := range s.states {
		wg.Add(1)
		go func() {
			defer wg.Done()
			outcomes[i] = s.collect(ctx, st, now)
		}()
	}
	wg.Wait()

	snap := Snapshot{
		Timestamp:  now,
		Collectors: make(map[string]any, len(s.states)),
		Series:     map[string]float64{},
	}
	for i, st := range s.states {
		name := st.c.Name()
		o := outcomes[i]
		s.logTransition(st, o.err)
		if o.err != nil {
			snap.Collectors[name] = errorSection(o.err)
			continue
		}
		st.prev = now
		snap.Collectors[name] = o.data
		for k, v := range o.series {
			snap.Series[k] = v
		}
	}

	s.latestMu.Lock()
	s.latest, s.hasValue = snap, true
	s.latestMu.Unlock()
	s.publish(snap)
	return snap
}

// collect runs one collector and waits for it at most s.timeout.
func (s *Sampler) collect(ctx context.Context, st *collectorState, now time.Time) outcome {
	if !st.busy.CompareAndSwap(false, true) {
		return outcome{err: errors.New("previous collection is still running")}
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	env := &Env{FS: s.fs, Runner: s.runner, Now: now, Prev: st.prev}
	done := make(chan outcome, 1) // buffered so a late collector can still finish
	go func() {
		defer st.busy.Store(false)
		done <- runCollector(ctx, st.c, env)
	}()

	select {
	case o := <-done:
		return o
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return outcome{err: fmt.Errorf("timed out after %v", s.timeout)}
		}
		return outcome{err: ctx.Err()}
	}
}

// runCollector calls Collect and encodes the result, turning panics and
// unencodable data into errors.
func runCollector(ctx context.Context, c Collector, env *Env) (o outcome) {
	defer func() {
		if r := recover(); r != nil {
			o = outcome{err: fmt.Errorf("panic: %v", r)}
		}
	}()

	res, err := c.Collect(ctx, env)
	if err != nil {
		return outcome{err: err}
	}
	data := res.Data
	if data == nil {
		data = struct{}{}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return outcome{err: fmt.Errorf("encode data: %w", err)}
	}
	series := make(map[string]float64, len(res.Series))
	for k, v := range res.Series {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			series[k] = v
		}
	}
	return outcome{data: raw, series: series}
}

func errorSection(err error) json.RawMessage {
	raw, _ := json.Marshal(map[string]string{"error": err.Error()})
	return raw
}

// logTransition logs a collector error only when it differs from the last
// one, and logs once when the collector recovers.
func (s *Sampler) logTransition(st *collectorState, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if msg == st.lastErr {
		return
	}
	switch {
	case msg != "":
		s.logger.Warn("collector failed", "collector", st.c.Name(), "err", msg)
	default:
		s.logger.Info("collector recovered", "collector", st.c.Name())
	}
	st.lastErr = msg
}

// Latest returns the most recent snapshot. ok is false before the first
// round has finished.
func (s *Sampler) Latest() (snap Snapshot, ok bool) {
	s.latestMu.RLock()
	defer s.latestMu.RUnlock()
	return s.latest, s.hasValue
}

// Subscribe returns a channel that receives every new snapshot, and a
// function that unsubscribes and closes the channel. A subscriber that falls
// more than SubscriberBuffer snapshots behind loses the oldest ones; it never
// slows down the sampler. The channel is also closed when Run returns.
func (s *Sampler) Subscribe() (<-chan Snapshot, func()) {
	ch := make(chan Snapshot, SubscriberBuffer)
	s.subMu.Lock()
	defer s.subMu.Unlock()
	if s.closed {
		close(ch)
		return ch, func() {}
	}
	s.subs[ch] = struct{}{}

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			s.subMu.Lock()
			defer s.subMu.Unlock()
			if _, ok := s.subs[ch]; ok {
				delete(s.subs, ch)
				close(ch)
			}
		})
	}
}

func (s *Sampler) publish(snap Snapshot) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for ch := range s.subs {
		for {
			select {
			case ch <- snap:
			default:
				// Full: drop the oldest unread snapshot and try again. The
				// subscriber may have read it meanwhile, which is fine too.
				select {
				case <-ch:
				default:
				}
				continue
			}
			break
		}
	}
}

func (s *Sampler) closeSubscribers() {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for ch := range s.subs {
		delete(s.subs, ch)
		close(ch)
	}
	s.closed = true
}
