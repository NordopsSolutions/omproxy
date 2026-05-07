package cache

import (
	"context"
	_ "embed"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schemaSQL string

type Store struct {
	pool *pgxpool.Pool
}

type Entry struct {
	CacheKey     string
	Kind         string
	Lat          float64
	Lon          float64
	ForecastDays int
	Timezone     string
	MetricsHash  string
	ResponseJSON []byte
	FetchedAt    time.Time
	ExpiresAt    time.Time
	SourceURL    string
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func InitSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, schemaSQL)
	return err
}

func (s *Store) Get(ctx context.Context, cacheKey string) (Entry, bool, error) {
	const query = `
SELECT cache_key, kind, lat, lon, forecast_days, timezone, metrics_hash, response_json,
       fetched_at, expires_at, source_url
FROM weather_api_cache
WHERE cache_key = $1`

	var entry Entry
	err := s.pool.QueryRow(ctx, query, cacheKey).Scan(
		&entry.CacheKey,
		&entry.Kind,
		&entry.Lat,
		&entry.Lon,
		&entry.ForecastDays,
		&entry.Timezone,
		&entry.MetricsHash,
		&entry.ResponseJSON,
		&entry.FetchedAt,
		&entry.ExpiresAt,
		&entry.SourceURL,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return Entry{}, false, nil
		}
		return Entry{}, false, err
	}
	return entry, true, nil
}

func (s *Store) Upsert(ctx context.Context, entry Entry, ttl time.Duration) (Entry, error) {
	const query = `
INSERT INTO weather_api_cache (
    cache_key,
    kind,
    lat,
    lon,
    forecast_days,
    timezone,
    metrics_hash,
    response_json,
    fetched_at,
    expires_at,
    source_url
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now(), now() + ($9::text)::interval, $10)
ON CONFLICT (cache_key)
DO UPDATE SET
    response_json = EXCLUDED.response_json,
    fetched_at = now(),
    expires_at = EXCLUDED.expires_at,
    source_url = EXCLUDED.source_url
RETURNING fetched_at, expires_at`

	ttlText := ttl.String()
	err := s.pool.QueryRow(
		ctx,
		query,
		entry.CacheKey,
		entry.Kind,
		entry.Lat,
		entry.Lon,
		entry.ForecastDays,
		entry.Timezone,
		entry.MetricsHash,
		entry.ResponseJSON,
		ttlText,
		entry.SourceURL,
	).Scan(&entry.FetchedAt, &entry.ExpiresAt)
	if err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func (s *Store) CleanupExpired(ctx context.Context, olderThan time.Duration) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM weather_api_cache WHERE expires_at < now() - ($1::text)::interval`, olderThan.String())
	return err
}
