package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"nordops/omproxy/internal/cache"
	"nordops/omproxy/internal/config"
	"nordops/omproxy/internal/openmeteo"
	"nordops/omproxy/internal/stats"
)

var (
	ErrBadRequest = errors.New("bad request")
	ErrNotFound   = errors.New("not found")
)

type Weather struct {
	cfg       config.Config
	cache     *cache.Store
	stats     *stats.Store
	openMeteo *openmeteo.Client
	now       func() time.Time
}

type WeatherResponse struct {
	Source    string         `json:"source"`
	Kind      string         `json:"kind"`
	Metric    string         `json:"metric,omitempty"`
	Lat       float64        `json:"lat"`
	Lon       float64        `json:"lon"`
	StartDate string         `json:"start_date,omitempty"`
	EndDate   string         `json:"end_date,omitempty"`
	FetchedAt time.Time      `json:"fetched_at"`
	ExpiresAt time.Time      `json:"expires_at"`
	Data      map[string]any `json:"data"`
}

func NewWeather(cfg config.Config, cacheStore *cache.Store, statsStore *stats.Store, openMeteoClient *openmeteo.Client) *Weather {
	return &Weather{
		cfg:       cfg,
		cache:     cacheStore,
		stats:     statsStore,
		openMeteo: openMeteoClient,
		now:       time.Now,
	}
}

func (w *Weather) Get(ctx context.Context, kind string, lat, lon float64, metric string, pastDays int) (WeatherResponse, error) {
	if kind != "hourly" && kind != "daily" {
		return WeatherResponse{}, fmt.Errorf("%w: invalid forecast kind", ErrBadRequest)
	}
	if err := validateCoordinates(lat, lon); err != nil {
		_ = w.stats.Record(ctx, stats.Event{Kind: kind, EventType: stats.EventBadRequest, Metric: metric, StatusCode: 400})
		return WeatherResponse{}, err
	}
	if pastDays < 0 || pastDays > 92 {
		return WeatherResponse{}, fmt.Errorf("%w: past_days must be between 0 and 92", ErrBadRequest)
	}

	metrics := w.metricsForKind(kind)
	if metric != "" && !containsMetric(metrics, metric) {
		nLat, nLon := lat, lon
		_ = w.stats.Record(ctx, stats.Event{Kind: kind, Lat: &nLat, Lon: &nLon, EventType: stats.EventBadRequest, Metric: metric, StatusCode: 400})
		return WeatherResponse{}, fmt.Errorf("%w: metric %q is not configured for %s", ErrBadRequest, metric, kind)
	}

	lat = roundCoordinate(lat, w.cfg.Cache.CoordinatePrecision)
	lon = roundCoordinate(lon, w.cfg.Cache.CoordinatePrecision)
	metricsHash := hashMetrics(metrics)
	cacheKey := buildCacheKey(kind, lat, lon, w.cfg.OpenMeteo.ForecastDays, pastDays, w.cfg.OpenMeteo.Timezone, metricsHash)

	entry, ok, err := w.cache.Get(ctx, cacheKey)
	if err != nil {
		return WeatherResponse{}, err
	}
	if ok && entry.ExpiresAt.After(w.now()) {
		_ = w.stats.Record(ctx, stats.Event{Kind: kind, Lat: &lat, Lon: &lon, EventType: stats.EventCacheHit, Metric: metric, StatusCode: 200})
		return w.buildResponse("cache", entry, metric)
	}

	_ = w.stats.Record(ctx, stats.Event{Kind: kind, Lat: &lat, Lon: &lon, EventType: stats.EventCacheMiss, Metric: metric, StatusCode: 200})

	fetched, err := w.openMeteo.Fetch(ctx, kind, lat, lon, metrics, w.cfg.OpenMeteo.ForecastDays, pastDays, w.cfg.OpenMeteo.Timezone)
	if err != nil {
		_ = w.stats.Record(ctx, stats.Event{Kind: kind, Lat: &lat, Lon: &lon, EventType: stats.EventOpenMeteoError, Metric: metric, StatusCode: 502})
		return WeatherResponse{}, err
	}

	entry = cache.Entry{
		CacheKey:     cacheKey,
		Kind:         kind,
		Lat:          lat,
		Lon:          lon,
		ForecastDays: w.cfg.OpenMeteo.ForecastDays,
		Timezone:     w.cfg.OpenMeteo.Timezone,
		MetricsHash:  metricsHash,
		ResponseJSON: fetched.Body,
		SourceURL:    fetched.SourceURL,
	}
	entry, err = w.cache.Upsert(ctx, entry, w.cfg.Cache.TTL.Duration)
	if err != nil {
		return WeatherResponse{}, err
	}
	_ = w.stats.Record(ctx, stats.Event{Kind: kind, Lat: &lat, Lon: &lon, EventType: stats.EventOpenMeteoFetch, Metric: metric, StatusCode: 200})

	return w.buildResponse("open_meteo", entry, metric)
}

func (w *Weather) metricsForKind(kind string) []string {
	if kind == "daily" || kind == "archive" {
		return w.cfg.Metrics.Daily
	}
	return w.cfg.Metrics.Hourly
}

// GetArchive returns historical daily data (ERA5 reanalysis) for the given
// date range. Cache TTL is much longer than forecast since ERA5 data is
// stable: 30 days for ranges ending more than 7 days ago, 6 hours for ranges
// that include very recent days (ERA5 lags ~5 days).
func (w *Weather) GetArchive(ctx context.Context, lat, lon float64, startDate, endDate, metric string) (WeatherResponse, error) {
	if err := validateCoordinates(lat, lon); err != nil {
		_ = w.stats.Record(ctx, stats.Event{Kind: "archive", EventType: stats.EventBadRequest, Metric: metric, StatusCode: 400})
		return WeatherResponse{}, err
	}
	if err := validateDateRange(startDate, endDate); err != nil {
		_ = w.stats.Record(ctx, stats.Event{Kind: "archive", EventType: stats.EventBadRequest, Metric: metric, StatusCode: 400})
		return WeatherResponse{}, err
	}

	metrics := w.cfg.Metrics.Daily
	if metric != "" && !containsMetric(metrics, metric) {
		nLat, nLon := lat, lon
		_ = w.stats.Record(ctx, stats.Event{Kind: "archive", Lat: &nLat, Lon: &nLon, EventType: stats.EventBadRequest, Metric: metric, StatusCode: 400})
		return WeatherResponse{}, fmt.Errorf("%w: metric %q is not configured for archive", ErrBadRequest, metric)
	}

	lat = roundCoordinate(lat, w.cfg.Cache.CoordinatePrecision)
	lon = roundCoordinate(lon, w.cfg.Cache.CoordinatePrecision)
	metricsHash := hashMetrics(metrics)
	cacheKey := buildArchiveCacheKey(lat, lon, startDate, endDate, w.cfg.OpenMeteo.Timezone, metricsHash)

	entry, ok, err := w.cache.Get(ctx, cacheKey)
	if err != nil {
		return WeatherResponse{}, err
	}
	if ok && entry.ExpiresAt.After(w.now()) {
		_ = w.stats.Record(ctx, stats.Event{Kind: "archive", Lat: &lat, Lon: &lon, EventType: stats.EventCacheHit, Metric: metric, StatusCode: 200})
		return w.buildArchiveResponse("cache", entry, startDate, endDate, metric)
	}

	_ = w.stats.Record(ctx, stats.Event{Kind: "archive", Lat: &lat, Lon: &lon, EventType: stats.EventCacheMiss, Metric: metric, StatusCode: 200})

	fetched, err := w.openMeteo.FetchArchive(ctx, lat, lon, metrics, startDate, endDate, w.cfg.OpenMeteo.Timezone)
	if err != nil {
		_ = w.stats.Record(ctx, stats.Event{Kind: "archive", Lat: &lat, Lon: &lon, EventType: stats.EventOpenMeteoError, Metric: metric, StatusCode: 502})
		return WeatherResponse{}, err
	}

	ttl := archiveTTL(endDate, w.now())
	entry = cache.Entry{
		CacheKey:     cacheKey,
		Kind:         "archive",
		Lat:          lat,
		Lon:          lon,
		ForecastDays: 0,
		Timezone:     w.cfg.OpenMeteo.Timezone,
		MetricsHash:  metricsHash,
		ResponseJSON: fetched.Body,
		SourceURL:    fetched.SourceURL,
	}
	entry, err = w.cache.Upsert(ctx, entry, ttl)
	if err != nil {
		return WeatherResponse{}, err
	}
	_ = w.stats.Record(ctx, stats.Event{Kind: "archive", Lat: &lat, Lon: &lon, EventType: stats.EventOpenMeteoFetch, Metric: metric, StatusCode: 200})

	return w.buildArchiveResponse("open_meteo", entry, startDate, endDate, metric)
}

func (w *Weather) buildArchiveResponse(source string, entry cache.Entry, startDate, endDate, metric string) (WeatherResponse, error) {
	data, err := extractData(entry.ResponseJSON, "daily", metric)
	if err != nil {
		return WeatherResponse{}, err
	}
	return WeatherResponse{
		Source:    source,
		Kind:      "archive",
		Metric:    metric,
		Lat:       entry.Lat,
		Lon:       entry.Lon,
		StartDate: startDate,
		EndDate:   endDate,
		FetchedAt: entry.FetchedAt,
		ExpiresAt: entry.ExpiresAt,
		Data:      data,
	}, nil
}

// validateDateRange parses YYYY-MM-DD start/end and ensures start<=end and
// span <= 11 years (more than enough for 10y climatology with a small buffer).
func validateDateRange(startDate, endDate string) error {
	if startDate == "" || endDate == "" {
		return fmt.Errorf("%w: start_date and end_date are required", ErrBadRequest)
	}
	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return fmt.Errorf("%w: start_date must be YYYY-MM-DD", ErrBadRequest)
	}
	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return fmt.Errorf("%w: end_date must be YYYY-MM-DD", ErrBadRequest)
	}
	if end.Before(start) {
		return fmt.Errorf("%w: end_date must be on or after start_date", ErrBadRequest)
	}
	const maxSpan = 366 * 11 * 24 * time.Hour
	if end.Sub(start) > maxSpan {
		return fmt.Errorf("%w: date span must be at most 11 years", ErrBadRequest)
	}
	return nil
}

func buildArchiveCacheKey(lat, lon float64, startDate, endDate, timezone, metricsHash string) string {
	return fmt.Sprintf(
		"openmeteo:archive:%.*f:%.*f:start:%s:end:%s:timezone:%s:metrics_hash:%s",
		8, lat, 8, lon, startDate, endDate, timezone, metricsHash,
	)
}

// archiveTTL picks how long to cache an archive response. ERA5 lags ~5 days
// from real-time, so very recent endings might still be revised — short TTL
// there. Older endings are stable — long TTL.
func archiveTTL(endDate string, now time.Time) time.Duration {
	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return 6 * time.Hour
	}
	if now.Sub(end) > 7*24*time.Hour {
		return 30 * 24 * time.Hour
	}
	return 6 * time.Hour
}

func (w *Weather) buildResponse(source string, entry cache.Entry, metric string) (WeatherResponse, error) {
	data, err := extractData(entry.ResponseJSON, entry.Kind, metric)
	if err != nil {
		return WeatherResponse{}, err
	}
	return WeatherResponse{
		Source:    source,
		Kind:      entry.Kind,
		Metric:    metric,
		Lat:       entry.Lat,
		Lon:       entry.Lon,
		FetchedAt: entry.FetchedAt,
		ExpiresAt: entry.ExpiresAt,
		Data:      data,
	}, nil
}

func validateCoordinates(lat, lon float64) error {
	if math.IsNaN(lat) || math.IsInf(lat, 0) || lat < -90 || lat > 90 {
		return fmt.Errorf("%w: lat must be between -90 and 90", ErrBadRequest)
	}
	if math.IsNaN(lon) || math.IsInf(lon, 0) || lon < -180 || lon > 180 {
		return fmt.Errorf("%w: lon must be between -180 and 180", ErrBadRequest)
	}
	return nil
}

func containsMetric(metrics []string, metric string) bool {
	for _, allowed := range metrics {
		if allowed == metric {
			return true
		}
	}
	return false
}

func roundCoordinate(value float64, precision int) float64 {
	pow := math.Pow10(precision)
	return math.Round(value*pow) / pow
}

func hashMetrics(metrics []string) string {
	sum := sha256.Sum256([]byte(strings.Join(metrics, ",")))
	return hex.EncodeToString(sum[:])[:12]
}

func buildCacheKey(kind string, lat, lon float64, forecastDays, pastDays int, timezone, metricsHash string) string {
	return fmt.Sprintf(
		"openmeteo:%s:%.*f:%.*f:days:%d:past:%d:timezone:%s:metrics_hash:%s",
		kind,
		8,
		lat,
		8,
		lon,
		forecastDays,
		pastDays,
		timezone,
		metricsHash,
	)
}

func extractData(raw []byte, kind, metric string) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var root map[string]any
	if err := decoder.Decode(&root); err != nil {
		return nil, err
	}
	section, ok := root[kind].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: open-meteo response missing %s section", ErrNotFound, kind)
	}
	if metric == "" {
		return section, nil
	}
	timeValues, ok := section["time"]
	if !ok {
		return nil, fmt.Errorf("%w: open-meteo response missing time field", ErrNotFound)
	}
	metricValues, ok := section[metric]
	if !ok {
		return nil, fmt.Errorf("%w: open-meteo response missing metric %q", ErrNotFound, metric)
	}
	return map[string]any{
		"time": timeValues,
		metric: metricValues,
	}, nil
}
