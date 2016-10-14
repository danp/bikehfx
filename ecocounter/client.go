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
	ResolutionHour = 2
	ResolutionDay  = 4
)

type Datapoint struct {
	Time  time.Time // local time
	Count int
}

const (
	dateFormat = "20060102"
)

func GetDatapoints(id string, begin, end time.Time, resolution Resolution) ([]Datapoint, error) {
	bs := begin.Format(dateFormat)
	es := end.Format(dateFormat)

	req, err := http.NewRequest(http.MethodGet, "http://www.eco-public.com/api/cw6Xk4jW4X4R/data/periode/"+id, nil)
	if err != nil {
		return nil, err
	}
	q := make(url.Values)
	q.Set("begin", bs)
	q.Set("end", es)
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

	var ds []Datapoint
	for _, b := range body {
		if b.Comptage == nil {
			continue
		}
		d := Datapoint{Time: time.Unix(b.Timestamp/1000, 0), Count: *b.Comptage}
		ds = append(ds, d)
	}

	return ds, nil
}
