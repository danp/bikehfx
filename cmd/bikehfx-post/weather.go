package main

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/dimchansky/utfbom"
	"github.com/graxinc/errutil"
)

type weather struct {
	max, min   float64
	rain, snow float64
}

type ecWeatherer struct {
}

func (ecWeatherer) weather(ctx context.Context, day time.Time) (weather, error) {
	u, err := url.Parse("https://climate.weather.gc.ca/climate_data/bulk_data_e.html")
	if err != nil {
		return weather{}, errutil.With(err)
	}
	q := u.Query()
	q.Set("format", "csv")
	q.Set("stationID", "50620")
	q.Set("Year", fmt.Sprintf("%d", day.Year()))
	q.Set("Month", fmt.Sprintf("%d", day.Month()))
	q.Set("Day", fmt.Sprintf("%d", day.Day()))
	q.Set("timeframe", "2")
	q.Set("submit", "Download Data")
	u.RawQuery = q.Encode()

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return weather{}, errutil.With(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return weather{}, errutil.With(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return weather{}, errutil.New(errutil.Tags{"code": resp.StatusCode})
	}

	cr := csv.NewReader(utfbom.SkipOnly(resp.Body))

	header, err := cr.Read()
	if err != nil {
		return weather{}, errutil.With(err)
	}
	headerIndexes := make(map[string]int)
	for i, h := range header {
		headerIndexes[h] = i
	}
	const dateHeader = "Date/Time"
	if _, ok := headerIndexes[dateHeader]; !ok {
		return weather{}, errutil.New(errutil.Tags{"msg": "could not find header " + dateHeader})
	}

	wantDate := day.Format("2006-01-02")

	var dateRow []string
	for {
		row, err := cr.Read()
		if errors.Is(err, io.EOF) {
			return weather{}, errutil.New(errutil.Tags{"msg": "could not find row for " + wantDate})
		}
		if err != nil {
			return weather{}, errutil.With(err)
		}

		if row[headerIndexes[dateHeader]] == wantDate {
			dateRow = row
			break
		}
	}

	const (
		maxTempHeader = "Max Temp (°C)"
		minTempHeader = "Min Temp (°C)"
		totalRain     = "Total Rain (mm)"
		totalSnow     = "Total Snow (cm)"
	)

	maxTempRaw := dateRow[headerIndexes[maxTempHeader]]
	minTempRaw := dateRow[headerIndexes[minTempHeader]]
	if maxTempRaw == "" || minTempRaw == "" {
		return weather{}, errutil.New(errutil.Tags{"msg": "could not find min/max temp for " + wantDate})
	}
	maxTemp, err := strconv.ParseFloat(maxTempRaw, 64)
	if err != nil {
		return weather{}, errutil.With(err)
	}
	minTemp, err := strconv.ParseFloat(minTempRaw, 64)
	if err != nil {
		return weather{}, errutil.With(err)
	}

	w := weather{
		min: minTemp,
		max: maxTemp,
	}

	rainRaw := dateRow[headerIndexes[totalRain]]
	if rainRaw != "" {
		rain, err := strconv.ParseFloat(rainRaw, 64)
		if err == nil && rain > 0 {
			w.rain = rain
		}
	}
	snowRaw := dateRow[headerIndexes[totalSnow]]
	if snowRaw != "" {
		snow, err := strconv.ParseFloat(snowRaw, 64)
		if err == nil && snow > 0 {
			w.snow = snow
		}
	}

	return w, nil
}
