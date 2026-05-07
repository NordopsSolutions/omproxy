CREATE TABLE IF NOT EXISTS weather_api_cache (
    cache_key text PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN ('hourly', 'daily')),
    lat double precision NOT NULL,
    lon double precision NOT NULL,
    forecast_days integer NOT NULL,
    timezone text NOT NULL,
    metrics_hash text NOT NULL,
    response_json jsonb NOT NULL,
    fetched_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    source_url text NOT NULL
);

CREATE INDEX IF NOT EXISTS weather_api_cache_expires_at_idx
    ON weather_api_cache (expires_at);

CREATE INDEX IF NOT EXISTS weather_api_cache_kind_lat_lon_idx
    ON weather_api_cache (kind, lat, lon);

CREATE TABLE IF NOT EXISTS weather_api_stats (
    id bigserial PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now(),
    day date NOT NULL DEFAULT CURRENT_DATE,
    kind text,
    lat double precision,
    lon double precision,
    event_type text NOT NULL CHECK (event_type IN (
        'cache_hit',
        'cache_miss',
        'open_meteo_fetch',
        'open_meteo_error',
        'bad_request'
    )),
    metric text,
    status_code integer
);

CREATE INDEX IF NOT EXISTS weather_api_stats_day_idx
    ON weather_api_stats (day);

CREATE INDEX IF NOT EXISTS weather_api_stats_event_type_idx
    ON weather_api_stats (event_type);
