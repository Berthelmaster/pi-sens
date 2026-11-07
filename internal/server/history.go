package server

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/history"
)

const (
	// maxHistorySeries caps the series in one /api/history request.
	maxHistorySeries = 10
	// defaultHistoryPoints is the points per series when the request does
	// not say.
	defaultHistoryPoints = 500
	// minHistoryPoints and maxHistoryPoints bound the points parameter.
	minHistoryPoints = 2
	maxHistoryPoints = 5000
	// maxUnix is the last second of the year 9999. Later times are refused
	// so range arithmetic cannot overflow.
	maxUnix = 253_402_300_799
)

type seriesResponse struct {
	Series []string `json:"series"`
}

// handleSeries lists the stored series keys, only those starting with the
// prefix parameter if there is one.
func (s *Server) handleSeries(w http.ResponseWriter, r *http.Request) {
	keys, err := s.history.SeriesKeys(r.Context(), r.URL.Query().Get("prefix"))
	if err != nil {
		s.historyFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, seriesResponse{Series: keys})
}

// handleHistory returns the history of up to maxHistorySeries series.
//
//	GET /api/history?series=a,b&from=<unix>&to=<unix>&points=<n>
//
// series may also be repeated. to defaults to now and points to
// defaultHistoryPoints.
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	q, err := s.parseHistoryQuery(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	res, err := s.history.Query(r.Context(), q)
	if err != nil {
		s.historyFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) parseHistoryQuery(r *http.Request) (history.Query, error) {
	params := r.URL.Query()

	var keys []string
	seen := map[string]bool{}
	for _, list := range params["series"] {
		for _, key := range strings.Split(list, ",") {
			key = strings.TrimSpace(key)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return history.Query{}, errors.New("series is required, a comma separated list of series keys")
	}
	if len(keys) > maxHistorySeries {
		return history.Query{}, fmt.Errorf("at most %d series per request, got %d", maxHistorySeries, len(keys))
	}

	if !params.Has("from") {
		return history.Query{}, errors.New("from is required, a unix time in seconds")
	}
	from, err := parseUnix(params.Get("from"))
	if err != nil {
		return history.Query{}, fmt.Errorf("from: %w", err)
	}
	to := s.now().Truncate(time.Second)
	if params.Has("to") {
		if to, err = parseUnix(params.Get("to")); err != nil {
			return history.Query{}, fmt.Errorf("to: %w", err)
		}
	}
	if !from.Before(to) {
		return history.Query{}, fmt.Errorf("from (%d) must be before to (%d)", from.Unix(), to.Unix())
	}

	points := defaultHistoryPoints
	if params.Has("points") {
		points, err = strconv.Atoi(params.Get("points"))
		if err != nil || points < minHistoryPoints || points > maxHistoryPoints {
			return history.Query{}, fmt.Errorf("points must be a whole number from %d to %d", minHistoryPoints, maxHistoryPoints)
		}
	}
	return history.Query{Keys: keys, From: from, To: to, Points: points}, nil
}

// parseUnix parses a unix time in whole seconds, from 1970 to the year 9999.
func parseUnix(v string) (time.Time, error) {
	sec, err := strconv.ParseInt(v, 10, 64)
	if err != nil || sec < 0 || sec > maxUnix {
		return time.Time{}, fmt.Errorf("%q is not a unix time in seconds", v)
	}
	return time.Unix(sec, 0), nil
}

// historyFailed answers 500 for a database error. A client that went away
// is not worth logging.
func (s *Server) historyFailed(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() == nil {
		s.logger.Error("history request failed", "path", r.URL.Path, "err", err)
	}
	writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "history query failed"})
}
