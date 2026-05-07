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
	baseURL string
	http    *http.Client
}

type Response struct {
	Body      []byte
	SourceURL string
}

func New(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: baseURL,
		http: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *Client) Fetch(ctx context.Context, kind string, lat, lon float64, metrics []string, forecastDays int, timezone string) (Response, error) {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return Response{}, err
	}
	query := u.Query()
	query.Set("latitude", strconv.FormatFloat(lat, 'f', -1, 64))
	query.Set("longitude", strconv.FormatFloat(lon, 'f', -1, 64))
	query.Set(kind, strings.Join(metrics, ","))
	query.Set("forecast_days", strconv.Itoa(forecastDays))
	query.Set("timezone", timezone)
	u.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
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
	return Response{Body: body, SourceURL: u.String()}, nil
}
