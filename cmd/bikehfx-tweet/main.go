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
			log.Fatal(err)
		}
		if len(cc) != 1 {
			continue
		}
		counts = append(counts, ccount{name: c.name, count: cc[0].Count})
		tot += cc[0].Count
	}
	if tot == 0 {
		log.Printf("no data for any counters on %s, doing nothing", day)
		return
	}

	sort.Slice(counts, func(i, j int) bool { return counts[j].count < counts[i].count })

	yf := day.Format("Mon Jan 2")
	stxt := fmt.Sprintf("%d #bikehfx trips counted on %s\n", tot, yf)
	for _, c := range counts {
		ctxt := fmt.Sprintf("\n%d %s", c.count, c.name)
		if len(stxt)+len(ctxt) > 135 {
			break
		}
		stxt += ctxt
	}
	log.Printf("at=tweet stxt=%q len=%d", stxt, len(stxt))

	// graph counters which support hourly resolution
	gb, err := makeHourlyGraph(day, counters)
	if err != nil {
		log.Fatal(err)
	}

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

	mid, err := uploadMedia(cl, gb)
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

func uploadMedia(cl *http.Client, m []byte) (int64, error) {
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

	err = json.NewDecoder(resp.Body).Decode(&mresp)
	return mresp.MediaID, err
}

type clientPublicQuerier struct {
	cl *ecocounter.Client
	id string
}

func (q clientPublicQuerier) query(day time.Time, resolution ecocounter.Resolution) ([]ecocounter.Datapoint, error) {
	return q.cl.GetDatapoints(q.id, day, day, resolution)
}
