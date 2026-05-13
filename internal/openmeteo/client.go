package openmeteo

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	baseURL    string
	archiveURL string
	http       *http.Client
}

type Response struct {
	Body      []byte
	SourceURL string
}

func New(baseURL, archiveURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL:    baseURL,
		archiveURL: archiveURL,
		http: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *Client) Fetch(ctx context.Context, kind string, lat, lon float64, metrics []string, forecastDays, pastDays int, timezone string) (Response, error) {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return Response{}, err
	}
	query := u.Query()
	query.Set("latitude", strconv.FormatFloat(lat, 'f', -1, 64))
	query.Set("longitude", strconv.FormatFloat(lon, 'f', -1, 64))
	query.Set(kind, strings.Join(metrics, ","))
	query.Set("forecast_days", strconv.Itoa(forecastDays))
	if pastDays > 0 {
		query.Set("past_days", strconv.Itoa(pastDays))
	}
	query.Set("timezone", timezone)
	u.RawQuery = query.Encode()

	return c.doGet(ctx, u.String())
}

// FetchArchive queries Open-Meteo's historical reanalysis (ERA5) endpoint for
// a fixed date range. Daily aggregations only — hourly archive data is
// possible but not yet exposed here.
func (c *Client) FetchArchive(ctx context.Context, lat, lon float64, dailyMetrics []string, startDate, endDate, timezone string) (Response, error) {
	u, err := url.Parse(c.archiveURL)
	if err != nil {
		return Response{}, err
	}
	query := u.Query()
	query.Set("latitude", strconv.FormatFloat(lat, 'f', -1, 64))
	query.Set("longitude", strconv.FormatFloat(lon, 'f', -1, 64))
	query.Set("start_date", startDate)
	query.Set("end_date", endDate)
	query.Set("daily", strings.Join(dailyMetrics, ","))
	query.Set("timezone", timezone)
	u.RawQuery = query.Encode()

	return c.doGet(ctx, u.String())
}

func (c *Client) doGet(ctx context.Context, fullURL string) (Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return Response{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return Response{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Response{}, fmt.Errorf("open-meteo returned %d: %s", resp.StatusCode, string(body))
	}
	return Response{Body: body, SourceURL: fullURL}, nil
}
