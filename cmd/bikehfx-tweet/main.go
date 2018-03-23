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

type counter struct {
	name  string
	ecoID string
}

var counters = []counter{
	{name: "Agri SB", ecoID: "100033965"},
	{name: "Uni Rowe", ecoID: "100033028"},
	{name: "Uni Arts", ecoID: "100036476"},
}

type Config struct {
	TwitterConsumerKey    string `env:"TWITTER_CONSUMER_KEY,requried"`
	TwitterConsumerSecret string `env:"TWITTER_CONSUMER_SECRET,required"`
	TwitterAppToken       string `env:"TWITTER_APP_TOKEN,required"`
	TwitterAppSecret      string `env:"TWITTER_APP_SECRET,required"`

	Day string `env:"DAY"`

	TestMode bool `env:"TEST_MODE"`
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	var cfg Config
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

	type ccount struct {
		name  string
		count int
	}
	counts := make([]ccount, 0, len(counters))

	var tot int
	for _, c := range counters {
		count, err := get(&ecl, c, day)
		if err != nil {
			log.Fatal(err)
		}
		counts = append(counts, ccount{name: c.name, count: count})
		tot += count
	}
	if tot == 0 {
		log.Printf("no data for any counters on %s, doing nothing", day)
		return
	}

	sort.Slice(counts, func(i, j int) bool { return counts[j].count < counts[i].count })

	yf := day.Format("Mon Jan 2")
	stxt := fmt.Sprintf("%d #bikehfx trips counted on %s\n", tot, yf)
	for _, c := range counts {
		if c.count == 0 {
			continue
		}

		ctxt := fmt.Sprintf("\n%d %s", c.count, c.name)
		if len(stxt)+len(ctxt) > 135 {
			break
		}
		stxt += ctxt
	}
	log.Printf("at=tweet stxt=%q", stxt)

	gb, err := makeHourlyGraph(&ecl, day)
	if err != nil {
		log.Fatal(err)
	}

	if cfg.TestMode {
		log.Println("test mode, writing graph.png")
		if err := ioutil.WriteFile("graph.png", gb, 0644); err != nil {
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

func get(cl *ecocounter.Client, c counter, day time.Time) (int, error) {
	ds, err := cl.GetDatapoints(c.ecoID, day, day, ecocounter.ResolutionDay)
	if err != nil {
		return 0, err
	}

	if len(ds) != 1 {
		return 0, nil
	}

	return ds[0].Count, nil
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
