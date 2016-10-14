package main

import (
	"errors"
	"fmt"
	"log"
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
	{name: "Agricola & North", ecoID: "101033965"},
}

type Config struct {
	TwitterConsumerKey    string `env:"TWITTER_CONSUMER_KEY,requried"`
	TwitterConsumerSecret string `env:"TWITTER_CONSUMER_SECRET,required"`
	TwitterAppToken       string `env:"TWITTER_APP_TOKEN,required"`
	TwitterAppSecret      string `env:"TWITTER_APP_SECRET,required"`
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	var cfg Config
	if err := envdecode.Decode(&cfg); err != nil {
		log.Fatal(err)
	}

	yesterday := time.Now().Add(-24 * time.Hour)

	var tot int
	for _, c := range counters {
		cnt, err := get(c, yesterday)
		if err != nil {
			log.Fatal(err)
		}
		tot += cnt
	}

	yf := yesterday.Format("Mon Jan 2")
	stxt := fmt.Sprintf("%d bike trips counted on %s\n\n#bikehfx", tot, yf)
	log.Printf("at=tweet stxt=%q", stxt)

	oaConfig := oauth1.NewConfig(cfg.TwitterConsumerKey, cfg.TwitterConsumerSecret)
	oaToken := oauth1.NewToken(cfg.TwitterAppToken, cfg.TwitterAppSecret)
	twc := twitter.NewClient(oaConfig.Client(oauth1.NoContext, oaToken))
	tw, _, err := twc.Statuses.Update(stxt, nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("https://twitter.com/" + tw.User.ScreenName + "/status/" + tw.IDStr)
}

func get(c counter, yesterday time.Time) (int, error) {
	ds, err := ecocounter.GetDatapoints(c.ecoID, yesterday, yesterday, ecocounter.ResolutionDay)
	if err != nil {
		return 0, err
	}

	if len(ds) != 1 {
		return 0, errors.New("no datapoints")
	}

	return ds[0].Count, nil
}
