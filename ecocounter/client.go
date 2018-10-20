package ecocounter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

// A Resolution is used when retrieving data using GetDatapoints.
type Resolution int

const (
	// ResolutionFifteenMinute requests data at 15 minute resolution.
	ResolutionFifteenMinute Resolution = 2
	// ResolutionHour requests hourly data.
	ResolutionHour Resolution = 3
	// ResolutionDay requests daily data.
	ResolutionDay Resolution = 4
)

const (
	// DefaultBaseURL is used by Client when Client.BaseURL is blank.
	// It's expected this URL will serve
	// GET /api/cw6Xk4jW4X4R/data/periode/{counter id} and
	// POST /ParcPublic/CounterData requests for GetDatapoints
	// and GetNonPublicDatapoints, respectively.
	DefaultBaseURL = "https://www.eco-public.com"
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

	nonPublicRequestDateFormat = "02/01/2006"
)

// Client is an eco counter API client.
//
// It uses request interactions gleaned from loading web pages and looking
// at Chrome network activity.
type Client struct {
	// Transport is the http.RoundTripper to use for making API requests.
	// If nil, http.DefaultTransport is used.
	Transport http.RoundTripper

	// BaseURL is the base URL to use for API requests.
	// If blank, DefaultBaseURL is used.
	// See documentation for DefaultBaseURL for request expectations.
	BaseURL string
}

// GetDatapoints returns datapoints for the given counter, between begin and end,
// at the provided resolution.
func (c Client) GetDatapoints(id string, begin, end time.Time, resolution Resolution) ([]Datapoint, error) {
	u, err := c.baseURL()
	if err != nil {
		return nil, err
	}

	u.Path = path.Join(u.Path, "/api/cw6Xk4jW4X4R/data/periode/"+id)

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("GetDatapoints: error making request for id %q: %s", id, err)
	}
	q := make(url.Values)
	q.Set("begin", begin.Format(requestDateFormat))
	q.Set("end", end.Format(requestDateFormat))
	q.Set("step", strconv.Itoa(int(resolution)))
	req.URL.RawQuery = q.Encode()

	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("GetDatapoints: error requesting data for id %q: %s", id, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GetDatapoints: bad status for id %q: %d", id, resp.StatusCode)
	}

	var body []struct {
		Date      string `json:"date"`
		Comptage  *int   `json:"comptage"`
		Timestamp int64  `json:"timestamp"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("GetDatapoints: decoding response body for id %q: %s", id, err)
	}

	ds := make([]Datapoint, 0)
	for _, b := range body {
		// Hourly data includes ticks on 15 minute intervals with no data,
		// we skip over those.
		if b.Comptage == nil {
			continue
		}

		t, err := time.Parse(responseDateFormat, b.Date)
		if err != nil {
			return nil, fmt.Errorf("GetDatapoints: parsing datapoint date %q for id %q: %s", id, b.Date, err)
		}

		d := Datapoint{Time: t.Format(responseDateFormat), Count: *b.Comptage}
		ds = append(ds, d)
	}

	return ds, nil
}

// GetNonPublicDatapoints gets daily datapoints for the given orgID (idOrganisme in request parameters),
// counterID (idPdc in request paramaters), and
// and directionIDs (pratiques in request parameters) between begin and end.
//
// This can be used for counters which do not have the "Public Web Page" option enabled.
func (c Client) GetNonPublicDatapoints(orgID, counterID string, directionIDs []string, begin, end time.Time) ([]Datapoint, error) {
	u, err := c.baseURL()
	if err != nil {
		return nil, err
	}
	u.Path = path.Join(u.Path, "/ParcPublic/CounterData")

	v := make(url.Values)
	v.Set("idOrganisme", orgID)
	v.Set("idPdc", counterID)
	v.Set("debut", begin.Format(nonPublicRequestDateFormat))
	v.Set("fin", end.Format(nonPublicRequestDateFormat))
	v.Set("interval", "3")
	v.Set("pratiques", strings.Join(directionIDs, ";"))

	req, err := http.NewRequest("POST", u.String(), strings.NewReader(v.Encode()))
	if err != nil {
		return nil, fmt.Errorf("GetNonPublicDatapoints: error making request for orgID %q and directionIDs %+v: %s", orgID, directionIDs, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("GetNonPublicDatapoints: error requesting data for orgID %q and directionIDs %+v: %s", orgID, directionIDs, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GetNonPublicDatapoints: bad status for orgID %q and directionIDs %+v: %d", orgID, directionIDs, resp.StatusCode)
	}

	// ugh
	// [["08\/28\/2017","245.0"],["08\/29\/2017","255.0"],255.0]
	var body []interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("GetNonPublicDatapoints: decoding response body for orgID %q and directionIDs %+v: %s", orgID, directionIDs, err)
	}

	var ds []Datapoint
	for _, x := range body {
		y, ok := x.([]interface{})
		if !ok {
			continue
		}

		dt := y[0].(string)
		d, err := time.Parse("01/02/2006", dt)
		if err != nil {
			return nil, fmt.Errorf("GetNonPublicDatapoints: parsing datapoint date %q for orgID %q and directionIDs %+v: %s", dt, orgID, directionIDs, err)
		}

		ct := y[1].(string)
		n, err := strconv.ParseFloat(ct, 32)
		if err != nil {
			return nil, fmt.Errorf("GetNonPublicDatapoints: parsing datapoint count %q for date %q and orgID %q and directionIDs %+v: %s", ct, dt, orgID, directionIDs, err)
		}

		dp := Datapoint{
			Time:  d.Format(responseDateFormat),
			Count: int(n),
		}
		ds = append(ds, dp)
	}

	return ds, nil
}

func (c Client) do(req *http.Request) (*http.Response, error) {
	tr := c.Transport
	if tr == nil {
		tr = http.DefaultTransport
	}
	return tr.RoundTrip(req)
}

func (c Client) baseURL() (*url.URL, error) {
	burl := c.BaseURL
	if burl == "" {
		burl = DefaultBaseURL
	}

	return url.Parse(burl)
}
