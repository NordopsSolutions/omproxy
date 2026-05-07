# Open-Meteo cache

Serviciu intern Go pentru cache lazy-load peste Open-Meteo, cu persistenta in PostgreSQL.

## Configurare locala

```bash
createdb weather
cp deploy/config.example.toml config.toml
go run ./cmd/openmeteo-cache -config config.toml
```

Configurarea implicita foloseste:

```text
postgres://weather:weather_dev@127.0.0.1:5432/weather?sslmode=disable
```

La pornire, serviciul creeaza automat tabelele `weather_api_cache` si `weather_api_stats`.

## Endpointuri

```bash
curl 'http://localhost:8080/api/v1/weather/hourly?lat=47.141&lon=23.878'
curl 'http://localhost:8080/api/v1/weather/hourly?lat=47.141&lon=23.878&metric=temperature_2m'
curl 'http://localhost:8080/api/v1/weather/daily?lat=47.141&lon=23.878'
curl 'http://localhost:8080/api/v1/weather/daily?lat=47.141&lon=23.878&metric=temperature_2m_max'
curl 'http://localhost:8080/api/v1/stats'
```
