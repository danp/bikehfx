package ecocounter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

type Resolution int

const (
	ResolutionHour Resolution = 2
	ResolutionDay  Resolution = 4
)

// A Datapoint represents a count at a point in time.
type Datapoint struct {
	// Time is the local time of the count, in YYYY-MM-DD HH:MM:SS format.
	Time string

	// Count is how many trips were counted.
	Count int
}

const (
	requestDateFormat  = "20060102"
	responseDateFormat = "2006-01-02 15:04:05"
)

func GetDatapoints(id string, begin, end time.Time, resolution Resolution) ([]Datapoint, error) {
	req, err := http.NewRequest(http.MethodGet, "http://www.eco-public.com/api/cw6Xk4jW4X4R/data/periode/"+id, nil)
	if err != nil {
		return nil, err
	}
	q := make(url.Values)
	q.Set("begin", begin.Format(requestDateFormat))
	q.Set("end", end.Format(requestDateFormat))
	q.Set("step", fmt.Sprintf("%d", resolution))
	req.URL.RawQuery = q.Encode()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad status, got %d", resp.StatusCode)
	}

	var body []struct {
		Date      string `json:"date"`
		Comptage  *int   `json:"comptage"`
		Timestamp int64  `json:"timestamp"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}

	ds := make([]Datapoint, 0)
	for _, b := range body {
		if b.Comptage == nil {
			continue
		}

		t, err := time.Parse(responseDateFormat, b.Date)
		if err != nil {
			return nil, err
		}

		d := Datapoint{Time: t.Format(responseDateFormat), Count: *b.Comptage}
		ds = append(ds, d)
	}

	return ds, nil
}
