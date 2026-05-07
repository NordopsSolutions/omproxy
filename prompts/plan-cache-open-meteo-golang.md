# Plan tehnic: cache Open-Meteo în Go

## 1. Scop

Aplicația va fi un serviciu intern, scris în Go, care expune endpointuri HTTP pentru date meteo provenite din Open-Meteo.

Datele se descarcă lazy-load:

1. Clientul cere date pentru coordonate și tip de forecast: hourly sau daily.
2. Serviciul verifică dacă există date valide în cache.
3. Dacă datele există și sunt în intervalul de valabilitate, implicit 1 oră, răspunde din cache.
4. Dacă datele nu există sau sunt expirate, serviciul descarcă datele de la Open-Meteo.
5. Datele noi sunt salvate în cache.
6. Dacă exista deja o intrare pentru aceleași coordonate și același tip de forecast, aceasta este rescrisă.

Serviciul nu este public. Va fi folosit intern de backend.

---

## 2. Tehnologii recomandate

### Limbaj

Go.

Motive:

- consum redus de resurse;
- deployment simplu ca binar sau container Docker;
- potrivit pentru servicii HTTP interne;
- bun pentru requesturi externe, cache și procesare JSON;
- ușor de integrat cu PostgreSQL.

### Stocare cache

PostgreSQL.

Motive:

- nu mai este nevoie de Redis pentru un cache simplu de 1 oră;
- cache-ul supraviețuiește la restart;
- datele pot fi inspectate ușor;
- statisticile pot fi obținute direct din baza de date;
- este suficient pentru volum mic sau mediu.

Redis poate fi adăugat ulterior dacă există volum mare de requesturi sau cerințe stricte de latență.

---

## 3. Configurare TOML

Fișier propus:

```text
config.toml
```

Exemplu:

```toml
[server]
listen_addr = "0.0.0.0:8080"
read_timeout = "10s"
write_timeout = "30s"
shutdown_timeout = "10s"

[open_meteo]
base_url = "https://api.open-meteo.com/v1/forecast"
timezone = "Europe/Bucharest"
forecast_days = 7
request_timeout = "20s"

[cache]
ttl = "1h"
coordinate_precision = 4

[database]
host = "127.0.0.1"
port = 5432
name = "weather"
user = "weather"
password = "change-me"
sslmode = "disable"
max_open_conns = 10
max_idle_conns = 5
conn_max_lifetime = "30m"

[metrics]
hourly = [
  "temperature_2m",
  "relative_humidity_2m",
  "precipitation_probability",
  "precipitation",
  "weather_code",
  "wind_speed_10m",
  "wind_gusts_10m",
  "wind_direction_10m",
  "pressure_msl",
  "et0_fao_evapotranspiration",
  "vapour_pressure_deficit",
  "soil_temperature_6cm",
  "soil_moisture_1_to_3cm",
  "shortwave_radiation"
]

daily = [
  "weather_code",
  "temperature_2m_max",
  "temperature_2m_min",
  "precipitation_sum",
  "precipitation_probability_max",
  "wind_speed_10m_max",
  "wind_gusts_10m_max",
  "et0_fao_evapotranspiration",
  "shortwave_radiation_sum"
]
```

Observații:

- `cache.ttl` controlează fereastra de cache, implicit 1 oră.
- `coordinate_precision` controlează rotunjirea coordonatelor pentru cheia de cache.
- `metrics.hourly` și `metrics.daily` definesc ce câmpuri se cer de la Open-Meteo.
- `forecast_days = 7` este o valoare potrivită pentru MVP.

---

## 4. Endpointuri HTTP

### 4.1 Hourly - toate metricile

```http
GET /api/v1/weather/hourly?lat=47.141&lon=23.878
```

Returnează toate metricile hourly definite în `config.toml`.

### 4.2 Hourly - o singură metrică

```http
GET /api/v1/weather/hourly?lat=47.141&lon=23.878&metric=temperature_2m
```

Returnează doar metrica cerută.

Dacă metrica nu este definită în lista `metrics.hourly`, se returnează:

```http
400 Bad Request
```

### 4.3 Daily - toate metricile

```http
GET /api/v1/weather/daily?lat=47.141&lon=23.878
```

Returnează toate metricile daily definite în `config.toml`.

### 4.4 Daily - o singură metrică

```http
GET /api/v1/weather/daily?lat=47.141&lon=23.878&metric=temperature_2m_max
```

Returnează doar metrica cerută.

Dacă metrica nu este definită în lista `metrics.daily`, se returnează:

```http
400 Bad Request
```

### 4.5 Statistici

```http
GET /api/v1/stats
```

Returnează statistici pe zile, de exemplu câte fetch-uri reale s-au făcut către Open-Meteo.

Exemplu răspuns:

```json
{
  "days": [
    {
      "date": "2026-05-05",
      "open_meteo_fetches": 18,
      "cache_hits": 143,
      "cache_misses": 18,
      "errors": 0
    }
  ]
}
```

---

## 5. Modelul de cache

### 5.1 Cheia de cache

Cheia trebuie să fie deterministă.

Format propus:

```text
openmeteo:{kind}:{lat}:{lon}:days:{forecast_days}:timezone:{timezone}:metrics_hash:{metrics_hash}
```

Unde:

- `kind` este `hourly` sau `daily`;
- `lat` și `lon` sunt coordonate rotunjite;
- `forecast_days` vine din configurare;
- `timezone` vine din configurare;
- `metrics_hash` este un hash al listei de metrici.

Exemplu:

```text
openmeteo:hourly:47.1410:23.8780:days:7:timezone:Europe/Bucharest:metrics_hash:9a12bc4d
```

De ce includem `metrics_hash`:

- dacă modifici lista metricilor în config, cache-ul vechi nu mai este folosit greșit;
- se evită răspunsuri incomplete când apar metrici noi.

### 5.2 Rotunjirea coordonatelor

Pentru `coordinate_precision = 4`:

```text
47.141234 -> 47.1412
23.878765 -> 23.8788
```

Precizie aproximativă:

- 3 zecimale: aproximativ 100 m;
- 4 zecimale: aproximativ 10 m;
- 5 zecimale: aproximativ 1 m.

Pentru meteo, 4 zecimale este suficient.

---

## 6. Schema PostgreSQL

### 6.1 Tabel cache

```sql
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
```

### 6.2 Tabel statistici requesturi

```sql
CREATE TABLE IF NOT EXISTS weather_api_stats (
    id bigserial PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now(),
    day date GENERATED ALWAYS AS (created_at::date) STORED,
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
```

---

## 7. Reguli de funcționare cache

### 7.1 Pentru fiecare request valid

1. Se validează `lat` și `lon`.
2. Se validează `metric`, dacă este transmis.
3. Se normalizează coordonatele prin rotunjire.
4. Se construiește cheia de cache.
5. Se caută în `weather_api_cache`.
6. Dacă `expires_at > now()`, se returnează din cache.
7. Dacă nu există sau este expirat:
   - se face request la Open-Meteo;
   - se salvează răspunsul cu `UPSERT`;
   - se returnează răspunsul nou.

### 7.2 Rescriere cache

La cache miss sau cache expired se face `UPSERT`:

```sql
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
VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, now(), now() + $9::interval, $10
)
ON CONFLICT (cache_key)
DO UPDATE SET
    response_json = EXCLUDED.response_json,
    fetched_at = now(),
    expires_at = EXCLUDED.expires_at,
    source_url = EXCLUDED.source_url;
```

---

## 8. Construirea URL-ului Open-Meteo

### 8.1 Hourly

```text
https://api.open-meteo.com/v1/forecast
  ?latitude=47.141
  &longitude=23.878
  &hourly=temperature_2m,relative_humidity_2m,...
  &forecast_days=7
  &timezone=Europe/Bucharest
```

### 8.2 Daily

```text
https://api.open-meteo.com/v1/forecast
  ?latitude=47.141
  &longitude=23.878
  &daily=temperature_2m_max,temperature_2m_min,...
  &forecast_days=7
  &timezone=Europe/Bucharest
```

Recomandare importantă:

- pentru endpointul hourly, cere doar metricile hourly;
- pentru endpointul daily, cere doar metricile daily;
- nu cere toate datele Open-Meteo dacă nu le folosești.

---

## 9. Format răspuns API

### 9.1 Răspuns cu toate metricile hourly

```json
{
  "source": "cache",
  "kind": "hourly",
  "lat": 47.141,
  "lon": 23.878,
  "fetched_at": "2026-05-05T10:00:00Z",
  "expires_at": "2026-05-05T11:00:00Z",
  "data": {
    "time": ["2026-05-05T00:00", "2026-05-05T01:00"],
    "temperature_2m": [12.4, 12.1],
    "relative_humidity_2m": [82, 84]
  }
}
```

### 9.2 Răspuns cu o singură metrică

```json
{
  "source": "cache",
  "kind": "hourly",
  "metric": "temperature_2m",
  "lat": 47.141,
  "lon": 23.878,
  "fetched_at": "2026-05-05T10:00:00Z",
  "expires_at": "2026-05-05T11:00:00Z",
  "data": {
    "time": ["2026-05-05T00:00", "2026-05-05T01:00"],
    "temperature_2m": [12.4, 12.1]
  }
}
```

---

## 10. Filtrarea unei singure metrici

Este preferabil să cache-uiești toate metricile definite pentru `hourly` sau `daily`, iar filtrarea pentru `metric=...` să se facă local.

Motiv:

- eviți multe chei de cache pentru fiecare metrică;
- un singur request Open-Meteo aduce toate metricile configurate;
- endpointurile cu `metric` devin doar o proiecție peste cache.

Regulă:

```text
GET /hourly fără metric -> returnează toate metricile hourly
GET /hourly cu metric -> returnează doar time + metrica cerută
```

La fel pentru daily.

---

## 11. Validări

### 11.1 Coordonate

Reguli:

```text
lat trebuie să fie între -90 și 90
lon trebuie să fie între -180 și 180
```

Dacă lipsesc sau sunt invalide:

```http
400 Bad Request
```

### 11.2 Metrică

Dacă `metric` este transmis:

- pentru hourly, trebuie să existe în `metrics.hourly`;
- pentru daily, trebuie să existe în `metrics.daily`.

Dacă nu există:

```http
400 Bad Request
```

### 11.3 Forecast days

În config:

```text
forecast_days trebuie să fie între 1 și 16
```

Pentru MVP:

```text
forecast_days = 7
```

---

## 12. Statistici

Endpoint:

```http
GET /api/v1/stats
```

Query SQL propus:

```sql
SELECT
    day,
    count(*) FILTER (WHERE event_type = 'open_meteo_fetch') AS open_meteo_fetches,
    count(*) FILTER (WHERE event_type = 'cache_hit') AS cache_hits,
    count(*) FILTER (WHERE event_type = 'cache_miss') AS cache_misses,
    count(*) FILTER (WHERE event_type = 'open_meteo_error') AS errors
FROM weather_api_stats
GROUP BY day
ORDER BY day DESC
LIMIT 30;
```

Statistici utile:

- câte requesturi au venit;
- câte au fost servite din cache;
- câte au mers la Open-Meteo;
- câte erori au apărut;
- raport cache hit/cache miss.

---

## 13. Structura proiectului Go

Structură propusă:

```text
openmeteo-cache/
├── cmd/
│   └── openmeteo-cache/
│       └── main.go
├── internal/
│   ├── config/
│   │   └── config.go
│   ├── httpapi/
│   │   ├── handlers.go
│   │   └── router.go
│   ├── openmeteo/
│   │   └── client.go
│   ├── cache/
│   │   └── postgres.go
│   ├── stats/
│   │   └── postgres.go
│   └── service/
│       └── weather.go
├── migrations/
│   └── 001_init.sql
├── config.example.toml
├── go.mod
└── README.md
```

---

## 14. Pachete Go recomandate

Pentru o implementare simplă:

```text
net/http                 - server HTTP standard
github.com/BurntSushi/toml - citire TOML
github.com/jackc/pgx/v5  - PostgreSQL driver
```

Opțional:

```text
github.com/go-chi/chi/v5 - router HTTP simplu
```

Pentru MVP, `net/http` este suficient.

---

## 15. Flow logic în serviciu

Pseudocod:

```text
HandleWeather(kind, lat, lon, optionalMetric):
    validate lat/lon
    validate metric against config

    normalizedLat = round(lat, coordinate_precision)
    normalizedLon = round(lon, coordinate_precision)

    metrics = config.metrics[kind]
    metricsHash = hash(metrics)
    cacheKey = buildCacheKey(kind, normalizedLat, normalizedLon, forecastDays, timezone, metricsHash)

    cacheEntry = cache.Get(cacheKey)

    if cacheEntry exists and cacheEntry.expires_at > now:
        stats.Record(cache_hit)
        return filterResponse(cacheEntry.response_json, optionalMetric)

    stats.Record(cache_miss)

    openMeteoResponse = openmeteo.Fetch(kind, normalizedLat, normalizedLon, metrics, forecastDays, timezone)

    if error:
        stats.Record(open_meteo_error)
        return 502 Bad Gateway

    cache.Upsert(cacheKey, openMeteoResponse, ttl)
    stats.Record(open_meteo_fetch)

    return filterResponse(openMeteoResponse, optionalMetric)
```

---

## 16. Concurență și protecție la requesturi simultane

Problemă posibilă:

Dacă vin 10 requesturi simultane pentru aceeași cheie expirată, toate pot face request către Open-Meteo.

Pentru MVP se poate accepta.

Pentru varianta mai bună, se adaugă protecție `singleflight`:

```text
golang.org/x/sync/singleflight
```

Astfel, pentru aceeași cheie de cache, doar primul request descarcă datele, iar celelalte așteaptă rezultatul.

Recomandare:

- pentru MVP: fără singleflight;
- pentru versiunea stabilă: adaugă singleflight.

---

## 17. Tratarea erorilor Open-Meteo

Dacă Open-Meteo nu răspunde:

### Variantă strictă

Returnezi:

```http
502 Bad Gateway
```

### Variantă recomandată ulterior

Dacă există cache expirat, poți returna datele expirate cu un flag:

```json
{
  "source": "stale_cache",
  "warning": "Open-Meteo fetch failed; returned expired cached data",
  "data": {}
}
```

Pentru MVP, păstrează varianta simplă:

```text
cache expirat + Open-Meteo eroare = 502
```

---

## 18. Cleanup cache

Cache-ul se poate curăța periodic.

Pentru început, nu este obligatoriu, deoarece `UPSERT` rescrie aceleași chei.

Totuși, dacă se cer multe coordonate diferite, adaugă cleanup:

```sql
DELETE FROM weather_api_cache
WHERE expires_at < now() - interval '7 days';
```

Poate rula:

- la pornirea aplicației;
- la fiecare 24h într-un goroutine;
- prin cron extern.

---

## 19. Securitate internă

Deoarece serviciul este intern:

- ascultă doar pe rețeaua internă, dacă se poate;
- nu expune public endpointurile;
- limitează accesul prin firewall sau reverse proxy;
- loghează requesturile fără să expui parole sau DSN complet;
- parola DB se citește din config sau variabilă de mediu.

Pentru producție, parola din TOML poate fi înlocuită cu:

```toml
password_env = "WEATHER_DB_PASSWORD"
```

---

## 20. Docker Compose minimal

Exemplu orientativ:

```yaml
services:
  openmeteo-cache:
    build: .
    container_name: openmeteo-cache
    restart: unless-stopped
    ports:
      - "8080:8080"
    volumes:
      - ./config.toml:/app/config.toml:ro

```

---

## 21. Ordinea recomandată de implementare

### Pasul 1

Creează structura proiectului Go și citirea `config.toml`.

### Pasul 2

Adaugă conectarea la PostgreSQL.

### Pasul 3

Rulează migrarea SQL pentru tabele.

### Pasul 4

Implementează clientul Open-Meteo.

### Pasul 5

Implementează funcțiile cache:

- `Get(cacheKey)`;
- `Upsert(cacheKey, responseJSON, ttl)`.

### Pasul 6

Implementează handlerul:

```text
GET /api/v1/weather/hourly
```

### Pasul 7

Adaugă filtrarea după `metric`.

### Pasul 8

Implementează:

```text
GET /api/v1/weather/daily
```

### Pasul 9

Adaugă logging și statistici.

### Pasul 10

Adaugă endpointul:

```text
GET /api/v1/stats
```

### Pasul 11

Adaugă teste cu `curl`.

### Pasul 12

Opțional: adaugă `singleflight`.

---

## 22. Testare manuală cu curl

### 22.1 Prima cerere hourly

```bash
curl 'http://localhost:8080/api/v1/weather/hourly?lat=47.141&lon=23.878'
```

Așteptat:

```text
source = open_meteo
```

### 22.2 A doua cerere hourly

```bash
curl 'http://localhost:8080/api/v1/weather/hourly?lat=47.141&lon=23.878'
```

Așteptat:

```text
source = cache
```

### 22.3 O singură metrică hourly

```bash
curl 'http://localhost:8080/api/v1/weather/hourly?lat=47.141&lon=23.878&metric=temperature_2m'
```

Așteptat:

```text
răspunsul conține doar time și temperature_2m
```

### 22.4 Daily

```bash
curl 'http://localhost:8080/api/v1/weather/daily?lat=47.141&lon=23.878'
```

### 22.5 Daily cu metrică

```bash
curl 'http://localhost:8080/api/v1/weather/daily?lat=47.141&lon=23.878&metric=temperature_2m_max'
```

### 22.6 Statistici

```bash
curl 'http://localhost:8080/api/v1/stats'
```

---

## 23. Decizie finală recomandată

Pentru MVP:

```text
Go service intern
PostgreSQL cache
config TOML
TTL implicit 1h
forecast_days = 7
lazy-load la primul request
UPSERT la cache miss sau cache expired
filtrare locală pentru metric=...
statistici în PostgreSQL
```

Aceasta este varianta simplă, stabilă și suficientă pentru un backend intern.

