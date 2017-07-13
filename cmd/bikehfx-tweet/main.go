package main

import (
	"fmt"
	"log"
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

	type ccount struct {
		name  string
		count int
	}
	counts := make([]ccount, 0, len(counters))

	var tot int
	for _, c := range counters {
		count, err := get(c, day)
		if err != nil {
			log.Fatal(err)
		}
		counts = append(counts, ccount{name: c.name, count: count})
		tot += count
	}
	if tot == 0 {
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

	if cfg.TestMode {
		log.Println("test mode, doing nothing")
		return
	}

	oaConfig := oauth1.NewConfig(cfg.TwitterConsumerKey, cfg.TwitterConsumerSecret)
	oaToken := oauth1.NewToken(cfg.TwitterAppToken, cfg.TwitterAppSecret)
	twc := twitter.NewClient(oaConfig.Client(oauth1.NoContext, oaToken))
	tw, _, err := twc.Statuses.Update(stxt, nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("https://twitter.com/" + tw.User.ScreenName + "/status/" + tw.IDStr)
}

func get(c counter, day time.Time) (int, error) {
	ds, err := ecocounter.GetDatapoints(c.ecoID, day, day, ecocounter.ResolutionDay)
	if err != nil {
		return 0, err
	}

	if len(ds) != 1 {
		return 0, nil
	}

	return ds[0].Count, nil
}
