package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"mime/multipart"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/danp/bikehfx/ecocounter"
	"github.com/dghubble/go-twitter/twitter"
	"github.com/dghubble/oauth1"
	"github.com/joeshaw/envdecode"
)

type countQuerier interface {
	query(day time.Time, resolution ecocounter.Resolution) ([]ecocounter.Datapoint, error)
}

type counter struct {
	name    string
	querier countQuerier
}

func main() {
	var cfg struct {
		TwitterConsumerKey    string `env:"TWITTER_CONSUMER_KEY,required"`
		TwitterConsumerSecret string `env:"TWITTER_CONSUMER_SECRET,required"`
		TwitterAppToken       string `env:"TWITTER_APP_TOKEN,required"`
		TwitterAppSecret      string `env:"TWITTER_APP_SECRET,required"`

		EcoVisio struct {
			Username string `env:"ECO_VISIO_USERNAME"`
			Password string `env:"ECO_VISIO_PASSWORD"`
			UserID   string `env:"ECO_VISIO_USER_ID"`
			DomainID string `env:"ECO_VISIO_DOMAIN_ID"`
		}

		Day string `env:"DAY"`

		TestMode bool `env:"TEST_MODE"`
	}
	if err := envdecode.Decode(&cfg); err != nil {
		log.Fatal(err)
	}

	day := time.Now().Add(-24 * time.Hour)
	if cfg.Day != "" {
		d, err := time.Parse("20060102", cfg.Day)
		if err != nil {
			log.Fatal(err)
		}
		day = d
	}

	var ecl ecocounter.Client
	// As of Aug 25, 2020, https://www.eco-public.com is not verifying.
	// Using a browser loads things via http, not https.
	ecl.BaseURL = "http://www.eco-public.com"

	counters := []counter{
		{name: "Uni Rowe", querier: clientPublicQuerier{&ecl, "100033028"}},
		{name: "Uni Arts", querier: clientPublicQuerier{&ecl, "100036476"}},
	}

	if cfg.EcoVisio.Username != "" && cfg.EcoVisio.Password != "" && cfg.EcoVisio.UserID != "" && cfg.EcoVisio.DomainID != "" {
		eva := newEcoVisioAuth(cfg.EcoVisio.Username, cfg.EcoVisio.Password, cfg.EcoVisio.UserID, cfg.EcoVisio.DomainID)

		counters = append(counters,
			counter{name: "South Park", querier: ecoVisioQuerier{eva, "100054257"}},
			counter{name: "Hollis", querier: ecoVisioQuerier{eva, "101059339"}},
		)
	}

	// load daily data for all counters
	type ccount struct {
		name  string
		count int
	}
	counts := make([]ccount, 0, len(counters))

	var tot int
	for _, c := range counters {
		cc, err := c.querier.query(day, ecocounter.ResolutionDay)
		if err != nil {
			log.Fatalf("querying %s: %s", c.name, err)
		}
		if len(cc) != 1 {
			continue
		}
		if cc[0].Count == 0 {
			continue
		}
		counts = append(counts, ccount{name: c.name, count: cc[0].Count})
		tot += cc[0].Count
	}
	if tot == 0 {
		log.Printf("no data for any counters on %s, doing nothing", day)
		return
	}

	sort.Slice(counts, func(i, j int) bool {
		if counts[i].count == counts[j].count {
			return counts[i].name < counts[j].name
		}
		return counts[i].count > counts[j].count
	})

	yf := day.Format("Mon Jan 2")
	stxt := fmt.Sprintf("%d #bikehfx trips counted on %s\n", tot, yf)
	for _, c := range counts {
		ctxt := fmt.Sprintf("\n%d %s", c.count, c.name)
		if len(stxt)+len(ctxt) > 135 {
			break
		}
		stxt += ctxt
	}

	// graph counters which support hourly resolution
	gb, atxt, err := makeHourlyGraph(day, counters)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("at=tweet stxt=%q slen=%d atxt=%q", stxt, len(stxt), atxt)

	if cfg.TestMode {
		log.Println("test mode, writing graph.png")
		if err := ioutil.WriteFile("graph.png", gb, 0600); err != nil {
			log.Fatal(err)
		}

		log.Println("test mode, not tweeting")
		return
	}

	oaConfig := oauth1.NewConfig(cfg.TwitterConsumerKey, cfg.TwitterConsumerSecret)
	oaToken := oauth1.NewToken(cfg.TwitterAppToken, cfg.TwitterAppSecret)
	cl := oaConfig.Client(oauth1.NoContext, oaToken)

	mid, err := uploadMedia(cl, gb, atxt)
	if err != nil {
		log.Fatal(err)
	}

	twc := twitter.NewClient(cl)
	tw, _, err := twc.Statuses.Update(stxt, &twitter.StatusUpdateParams{MediaIds: []int64{mid}})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("https://twitter.com/" + tw.User.ScreenName + "/status/" + tw.IDStr)
}

func uploadMedia(cl *http.Client, m []byte, altText string) (int64, error) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)

	fw, err := w.CreateFormField("media")
	if err != nil {
		return 0, err
	}
	if _, err := fw.Write(m); err != nil {
		return 0, err
	}
	if err := w.Close(); err != nil {
		return 0, err
	}

	req, err := http.NewRequest("POST", "https://upload.twitter.com/1.1/media/upload.json", &b)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := cl.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return 0, fmt.Errorf("got status %d", resp.StatusCode)
	}

	var mresp struct {
		MediaID int64 `json:"media_id"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&mresp); err != nil {
		return 0, err
	}

	if altText == "" {
		return mresp.MediaID, nil
	}

	var reqb struct {
		MediaID string `json:"media_id"`
		AltText struct {
			Text string `json:"text"`
		} `json:"alt_text"`
	}
	reqb.MediaID = strconv.FormatInt(mresp.MediaID, 10)
	reqb.AltText.Text = altText

	rb, err := json.Marshal(reqb)
	if err != nil {
		return 0, err
	}

	req, err = http.NewRequest("POST", "https://upload.twitter.com/1.1/media/metadata/create.json", bytes.NewReader(rb))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")

	resp, err = cl.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return 0, fmt.Errorf("got status %d", resp.StatusCode)
	}

	return mresp.MediaID, nil
}

type clientPublicQuerier struct {
	cl *ecocounter.Client
	id string
}

func (q clientPublicQuerier) query(day time.Time, resolution ecocounter.Resolution) ([]ecocounter.Datapoint, error) {
	return q.cl.GetDatapoints(q.id, day, day, resolution)
}

type ecoVisioAuth struct {
	username, password, userID, domainID string

	// should support expiry, etc
	tokenCh chan string
}

func newEcoVisioAuth(username, password, userID, domainID string) *ecoVisioAuth {
	ch := make(chan string, 1)
	ch <- ""
	return &ecoVisioAuth{
		username: username,
		password: password,
		userID:   userID,
		domainID: domainID,
		tokenCh:  ch,
	}
}

func (a *ecoVisioAuth) token() (string, error) {
	tok := <-a.tokenCh
	if tok == "" {
		t, err := a.auth()
		if err != nil {
			a.tokenCh <- ""
			return "", err
		}
		tok = t
	}
	a.tokenCh <- tok
	return tok, nil
}

func (a *ecoVisioAuth) auth() (string, error) {
	reqs := struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}{
		Login:    a.username,
		Password: a.password,
	}
	reqb, err := json.Marshal(reqs)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", "https://www.eco-visio.net/api/aladdin/1.0.0/connect", bytes.NewReader(reqb))
	if err != nil {
		return "", err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:80.0) Gecko/20100101 Firefox/80.0")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Origin", "https://www.eco-visio.net")
	req.Header.Set("DNT", "1")
	req.Header.Set("Referer", "https://www.eco-visio.net/v5/")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading eco visio auth response: %w", err)
	}

	if resp.StatusCode/100 != 2 {
		if len(b) > 100 {
			b = b[:100]
		}
		return "", fmt.Errorf("bad status %d for eco visio auth: %s", resp.StatusCode, b)
	}

	var resps struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(b, &resps); err != nil {
		return "", fmt.Errorf("decoding eco visio auth response: %w", err)
	}

	return resps.AccessToken, nil
}

type ecoVisioQuerier struct {
	auth *ecoVisioAuth
	id   string
}

func (q ecoVisioQuerier) query(day time.Time, resolution ecocounter.Resolution) ([]ecocounter.Datapoint, error) {
	var ress string
	switch resolution {
	case ecocounter.ResolutionDay:
		ress = "day"
	case ecocounter.ResolutionHour:
		ress = "1hour"
	}
	if ress == "" {
		return nil, nil
	}

	begin, end := day.Format("2006-01-02"), day.AddDate(0, 0, 1).Format("2006-01-02")
	bu := "https://www.eco-visio.net/api/aladdin/1.0.0/domain/" + q.auth.domainID + "/user/" + q.auth.userID + "/query/from/" + begin + "%2000:00/to/" + end + "%2000:00/by/" + ress

	var reqs struct {
		Flows []int `json:"flows"`
	}
	idi, err := strconv.Atoi(q.id)
	if err != nil {
		return nil, err
	}
	reqs.Flows = []int{idi}
	reqb, err := json.Marshal(reqs)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", bu, bytes.NewReader(reqb))
	if err != nil {
		return nil, err
	}

	tok, err := q.auth.token()
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:80.0) Gecko/20100101 Firefox/80.0")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Origin", "https://www.eco-visio.net")
	req.Header.Set("DNT", "1")
	req.Header.Set("Referer", "https://www.eco-visio.net/v5/")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading query response for %s: %w", q.id, err)
	}

	if resp.StatusCode/100 != 2 {
		if len(b) > 100 {
			b = b[:100]
		}
		return nil, fmt.Errorf("bad status %d querying %s: %s", resp.StatusCode, q.id, b)
	}

	var resps map[string]struct {
		Countdata [][]interface{}
	}
	if err := json.Unmarshal(b, &resps); err != nil {
		return nil, fmt.Errorf("unmarshaling query response for %s: %w", q.id, err)
	}

	ce, ok := resps[q.id]
	if !ok {
		return nil, nil
	}

	ds := make([]ecocounter.Datapoint, 0, len(ce.Countdata))
	for _, rp := range ce.Countdata {
		dp := ecocounter.Datapoint{
			Time:  rp[0].(string),
			Count: int(rp[1].(float64)),
		}
		ds = append(ds, dp)
	}

	return ds, nil
}
