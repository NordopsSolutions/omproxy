package stats

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	EventCacheHit       = "cache_hit"
	EventCacheMiss      = "cache_miss"
	EventOpenMeteoFetch = "open_meteo_fetch"
	EventOpenMeteoError = "open_meteo_error"
	EventBadRequest     = "bad_request"
)

type Store struct {
	pool *pgxpool.Pool
}

type Event struct {
	Kind       string
	Lat        *float64
	Lon        *float64
	EventType  string
	Metric     string
	StatusCode int
}

type DailyStat struct {
	Date             time.Time `json:"date"`
	OpenMeteoFetches int64     `json:"open_meteo_fetches"`
	CacheHits        int64     `json:"cache_hits"`
	CacheMisses      int64     `json:"cache_misses"`
	Errors           int64     `json:"errors"`
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Record(ctx context.Context, event Event) error {
	_, err := s.pool.Exec(
		ctx,
		`INSERT INTO weather_api_stats (kind, lat, lon, event_type, metric, status_code)
VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6)`,
		event.Kind,
		event.Lat,
		event.Lon,
		event.EventType,
		event.Metric,
		event.StatusCode,
	)
	return err
}

func (s *Store) Daily(ctx context.Context, limit int) ([]DailyStat, error) {
	if limit <= 0 || limit > 365 {
		limit = 30
	}
	rows, err := s.pool.Query(
		ctx,
		`SELECT
    day,
    count(*) FILTER (WHERE event_type = 'open_meteo_fetch') AS open_meteo_fetches,
    count(*) FILTER (WHERE event_type = 'cache_hit') AS cache_hits,
    count(*) FILTER (WHERE event_type = 'cache_miss') AS cache_misses,
    count(*) FILTER (WHERE event_type = 'open_meteo_error') AS errors
FROM weather_api_stats
GROUP BY day
ORDER BY day DESC
LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var days []DailyStat
	for rows.Next() {
		var day DailyStat
		if err := rows.Scan(&day.Date, &day.OpenMeteoFetches, &day.CacheHits, &day.CacheMisses, &day.Errors); err != nil {
			return nil, err
		}
		days = append(days, day)
	}
	return days, rows.Err()
}
