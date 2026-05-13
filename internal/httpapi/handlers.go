package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"nordops/omproxy/internal/service"
	"nordops/omproxy/internal/stats"
)

type API struct {
	weather *service.Weather
	stats   *stats.Store
	logger  *slog.Logger
}

type errorResponse struct {
	Error string `json:"error"`
}

func New(weather *service.Weather, statsStore *stats.Store, logger *slog.Logger) *API {
	return &API{weather: weather, stats: statsStore, logger: logger}
}

func (a *API) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("GET /api/v1/weather/hourly", a.weatherHandler("hourly"))
	mux.HandleFunc("GET /api/v1/weather/daily", a.weatherHandler("daily"))
	mux.HandleFunc("GET /api/v1/weather/archive", a.archiveHandler)
	mux.HandleFunc("GET /api/v1/stats", a.statsHandler)
	return logRequests(a.logger, mux)
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) weatherHandler(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		lat, err := parseRequiredFloat(query.Get("lat"), "lat")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
			return
		}
		lon, err := parseRequiredFloat(query.Get("lon"), "lon")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
			return
		}
		metric := query.Get("metric")

		pastDays := 0
		if rawPast := query.Get("past_days"); rawPast != "" {
			parsed, perr := strconv.Atoi(rawPast)
			if perr != nil {
				writeJSON(w, http.StatusBadRequest, errorResponse{Error: "past_days must be an integer"})
				return
			}
			pastDays = parsed
		}

		resp, err := a.weather.Get(r.Context(), kind, lat, lon, metric, pastDays)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, service.ErrBadRequest) {
				status = http.StatusBadRequest
			} else if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			} else {
				status = http.StatusBadGateway
			}
			writeJSON(w, status, errorResponse{Error: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func (a *API) archiveHandler(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	lat, err := parseRequiredFloat(query.Get("lat"), "lat")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	lon, err := parseRequiredFloat(query.Get("lon"), "lon")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	startDate := query.Get("start_date")
	endDate := query.Get("end_date")
	metric := query.Get("metric")

	resp, err := a.weather.GetArchive(r.Context(), lat, lon, startDate, endDate, metric)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, service.ErrBadRequest) {
			status = http.StatusBadRequest
		} else if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		} else {
			status = http.StatusBadGateway
		}
		writeJSON(w, status, errorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) statsHandler(w http.ResponseWriter, r *http.Request) {
	limit := 30
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "limit must be an integer"})
			return
		}
		limit = parsed
	}
	days, err := a.stats.Daily(r.Context(), limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": days})
}

func parseRequiredFloat(raw, name string) (float64, error) {
	if raw == "" {
		return 0, errors.New(name + " is required")
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, errors.New(name + " must be a number")
	}
	return value, nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func logRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		logger.Info("http request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started).String())
	})
}
