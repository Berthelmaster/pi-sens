package server

import (
	"math"
	"net/http"
	"strconv"
	"time"
)

const (
	// energyTodayKey is the series the energy collector reports the kWh used
	// so far today in. It climbs during the day and restarts from zero at
	// local midnight, so a day's highest value is its total.
	energyTodayKey = "energy.today_kwh"
	// defaultEnergyDays is the days /api/energy/daily returns when the
	// request does not say.
	defaultEnergyDays = 30
	// maxEnergyDays bounds the days parameter, a year and a day.
	maxEnergyDays = 366
)

type energyDay struct {
	// Date is the local calendar day, "2026-11-07".
	Date string `json:"date"`
	// Start is the day's local midnight in unix seconds.
	Start int64 `json:"start"`
	// KWh is the energy used that day, null when no samples are stored for
	// it. Today's counts up to now.
	KWh *float64 `json:"kwh"`
	// Cost is KWh at the configured price, left out without a price or kWh.
	Cost *float64 `json:"cost,omitempty"`
}

type energyDailyResponse struct {
	// Days is every day in the range, oldest first, ending with today.
	Days []energyDay `json:"days"`
	// Price and Currency are the configured price per kWh, left out when
	// none is set.
	Price    *float64 `json:"price,omitempty"`
	Currency *string  `json:"currency,omitempty"`
}

// handleEnergyDaily returns the energy used per local calendar day over the
// last days days, today included.
//
//	GET /api/energy/daily?days=<n>
//
// days defaults to defaultEnergyDays. A day's kWh is the highest
// energy.today_kwh stored for it, which is where the counter stood when the
// day ended.
func (s *Server) handleEnergyDaily(w http.ResponseWriter, r *http.Request) {
	days := defaultEnergyDays
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxEnergyDays {
			writeJSON(w, http.StatusBadRequest, errorResponse{
				Error: "days must be a whole number from 1 to " + strconv.Itoa(maxEnergyDays),
			})
			return
		}
		days = n
	}

	to := s.now().Truncate(time.Second).Add(time.Second)
	y, m, d := to.In(s.loc).Date()
	from := time.Date(y, m, d-days+1, 0, 0, 0, 0, s.loc)
	list, err := s.history.DailyMax(r.Context(), energyTodayKey, from, to, s.loc)
	if err != nil {
		s.historyFailed(w, r, err)
		return
	}

	res := energyDailyResponse{Days: make([]energyDay, 0, len(list))}
	if s.energyPrice > 0 {
		price, currency := s.energyPrice, s.energyCurrency
		res.Price, res.Currency = &price, &currency
	}
	for _, day := range list {
		out := energyDay{Date: day.Start.Format(time.DateOnly), Start: day.Start.Unix()}
		if day.OK {
			kwh := day.Max
			out.KWh = &kwh
			if s.energyPrice > 0 {
				cost := math.Round(kwh*s.energyPrice*1e4) / 1e4
				out.Cost = &cost
			}
		}
		res.Days = append(res.Days, out)
	}
	writeJSON(w, http.StatusOK, res)
}
