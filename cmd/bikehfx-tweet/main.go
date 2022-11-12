package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/danp/counterbase/directory"
	"github.com/danp/counterbase/query"
	"github.com/peterbourgon/ff/v3"
	"github.com/peterbourgon/ff/v3/ffcli"
	"golang.org/x/image/font/opentype"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/font"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/plotutil"
	"gonum.org/v1/plot/vg"
)

func main() {
	rootCmd, rootCfg := newRootCmd()

	rootCmd.Subcommands = append(rootCmd.Subcommands,
		newDailyCmd(rootCfg),
		newWeeklyCmd(rootCfg),
		newMonthlyCmd(rootCfg),
		newYearlyCmd(rootCfg),
	)

	if err := rootCmd.Parse(os.Args[1:]); err != nil {
		log.Fatal(err)
	}

	dir, err := loadDirectory(rootCfg.directoryURL)
	if err != nil {
		log.Fatal(err)
	}

	rootCfg.ccd = cyclingeCounterDirectoryWrapper{dir: dir}

	qu := &query.Client{
		URL: rootCfg.queryURL,
	}

	rootCfg.trq = counterbaseTimeRangeQuerier{querier: qu}

	rootCfg.rc = counterbaseRecordsChecker{
		qu:  qu,
		ccd: rootCfg.ccd,
	}

	var tt tweetThread
	if rootCfg.testMode {
		tt = tweetThreader{t: &saveTweeter{}, inReplyTo: rootCfg.tweetInReplyTo, initial: rootCfg.initialTweet}
	} else {
		var mtt multiTweetThreader

		if rootCfg.twitterAppSecret != "" {
			tw, err := newTwitterTweeter(rootCfg.twitterConsumerKey, rootCfg.twitterConsumerSecret, rootCfg.twitterAppToken, rootCfg.twitterAppSecret)
			if err != nil {
				log.Fatal(err)
			}
			mtt = append(mtt, tweetThreader{t: tw, inReplyTo: rootCfg.tweetInReplyTo, initial: rootCfg.initialTweet})
		}

		if rootCfg.mastodonClientID != "" {
			if rootCfg.tweetInReplyTo != "" {
				log.Fatal("not yet supported, need to break things apart more")
			}

			mt, err := newMastodonTooter(rootCfg.mastodonServer, rootCfg.mastodonClientID, rootCfg.mastodonClientSecret, rootCfg.mastodonAccessToken)
			if err != nil {
				log.Fatal(err)
			}

			mtt = append(mtt, tweetThreader{t: mt, inReplyTo: rootCfg.tweetInReplyTo, initial: rootCfg.initialTweet})
		}

		tt = mtt
	}

	rootCfg.twt = tt

	if err := rootCmd.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

type rootConfig struct {
	directoryURL string
	queryURL     string

	twitterConsumerKey    string
	twitterConsumerSecret string
	twitterAppToken       string
	twitterAppSecret      string
	tweetInReplyTo        string

	mastodonServer       string
	mastodonClientID     string
	mastodonClientSecret string
	mastodonAccessToken  string

	initialTweet string

	testMode bool

	ccd cyclingCounterDirectory
	trq timeRangeQuerier
	rc  recordsChecker
	twt tweetThread
}

func newRootCmd() (*ffcli.Command, *rootConfig) {
	var cfg rootConfig

	fs := flag.NewFlagSet("bikehfx-tweet", flag.ExitOnError)

	fs.StringVar(&cfg.directoryURL, "directory-url", "", "directory URL")
	fs.StringVar(&cfg.queryURL, "query-url", "", "query URL")

	fs.StringVar(&cfg.twitterConsumerKey, "twitter-consumer-key", "", "twitter consumer key")
	fs.StringVar(&cfg.twitterConsumerSecret, "twitter-consumer-secret", "", "twitter consumer secret")
	fs.StringVar(&cfg.twitterAppToken, "twitter-app-token", "", "twitter app token")
	fs.StringVar(&cfg.twitterAppSecret, "twitter-app-secret", "", "twitter app secret")
	fs.StringVar(&cfg.tweetInReplyTo, "tweet-in-reply-to", "", "if set, first tweet will reply to this status")
	fs.StringVar(&cfg.initialTweet, "initial-tweet", "", "if set, text for first tweet")

	fs.StringVar(&cfg.mastodonServer, "mastodon-server", "", "mastodon server URL")
	// https://docs.joinmastodon.org/client/token/, requires read:accounts, write:media, write:statuses
	fs.StringVar(&cfg.mastodonClientID, "mastodon-client-id", "", "mastodon client id/key")
	fs.StringVar(&cfg.mastodonClientSecret, "mastodon-client-secret", "", "mastodon client secret")
	fs.StringVar(&cfg.mastodonAccessToken, "mastodon-access-token", "", "mastodon access token")

	fs.BoolVar(&cfg.testMode, "test-mode", false, "if enabled, write generated tweets to disk instead of tweeting")

	return &ffcli.Command{
		ShortUsage: "bikehfx-tweet [flags] <subcommand>",
		FlagSet:    fs,
		Options:    []ff.Option{ff.WithEnvVarNoPrefix()},
		Exec: func(ctx context.Context, args []string) error {
			return flag.ErrHelp
		},
	}, &cfg
}

func loadDirectory(src string) (Directory, error) {
	var counters []directory.Counter

	u, err := url.Parse(src)
	if err != nil {
		return nil, err
	}

	switch u.Scheme {
	case "file":
		src = u.Path

		f, err := os.Open(src)
		if err != nil {
			return nil, err
		}
		defer f.Close()

		if err := json.NewDecoder(f).Decode(&counters); err != nil {
			return nil, err
		}
	case "http", "https":
		resp, err := http.Get(src) //nolint:gosec
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("-directory-url: bad status %d", resp.StatusCode)
		}

		if err := json.NewDecoder(resp.Body).Decode(&counters); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("-directory-url: unsupported scheme %q", u.Scheme)
	}

	return &fakeDirectory{C: counters}, nil
}

type tweetThread interface {
	tweetThread(context.Context, []tweet) ([]string, error)
}

type tweeter interface {
	tweet(context.Context, tweet) (string, error)
}

type Directory interface {
	Counters(context.Context) ([]directory.Counter, error)
}

type Querier interface {
	Query(ctx context.Context, q string) ([]query.Point, error)
}

//go:embed Arial.ttf
var arialBytes []byte

var (
	initGraphOnce sync.Once
	initGraphErr  error
)

func initGraph() error {
	initGraphOnce.Do(func() {
		arialTTF, err := opentype.Parse(arialBytes)
		if err != nil {
			initGraphErr = err
			return
		}
		arial := font.Font{Typeface: "Arial"}
		font.DefaultCache.Add([]font.Face{
			{
				Font: arial,
				Face: arialTTF,
			},
		})
		plot.DefaultFont = arial
		plotter.DefaultFont = arial
	})
	return initGraphErr
}

type fakeDirectory struct {
	C []directory.Counter
}

func (f fakeDirectory) Counters(ctx context.Context) ([]directory.Counter, error) {
	return f.C, nil
}

type commaSeparatedString struct {
	vals []string
}

func (c *commaSeparatedString) Set(s string) error {
	c.vals = strings.Split(s, ",")
	return nil
}

func (c *commaSeparatedString) String() string {
	return strings.Join(c.vals, ",")
}

type timeRange struct {
	begin, end time.Time // [begin, end)
}

func newTimeRangeDate(begin time.Time, years, months, days int) timeRange {
	return timeRange{begin: begin, end: begin.AddDate(years, months, days)}
}

func newTimeRangeDuration(begin time.Time, d time.Duration) timeRange {
	return timeRange{begin: begin, end: begin.Add(d)}
}

func (r timeRange) addDate(years, months, days int) timeRange {
	return timeRange{begin: r.begin.AddDate(years, months, days), end: r.end.AddDate(years, months, days)}
}

func (r timeRange) add(d time.Duration) timeRange {
	return timeRange{begin: r.begin.Add(d), end: r.end.Add(d)}
}

func (r timeRange) splitDate(years, months, days int) []timeRange {
	end := r.end
	r = newTimeRangeDate(r.begin, years, months, days)

	var out []timeRange
	for r.begin.Before(end) {
		out = append(out, r)
		r = r.addDate(years, months, days)
	}
	return out
}

func (r timeRange) split(d time.Duration) []timeRange {
	end := r.end
	r = newTimeRangeDuration(r.begin, d)

	var out []timeRange
	for r.begin.Before(end) {
		out = append(out, r)
		r = r.add(d)
	}
	return out
}

type timeRangeValue struct {
	tr  timeRange
	val int
}

func timeRangeBarGraph(trvs []timeRangeValue, title string, labeler func(timeRange) string) ([]byte, error) {
	if err := initGraph(); err != nil {
		return nil, err
	}
	plotutil.DefaultColors = plotutil.DarkColors

	p := plot.New()

	p.Title.Text = title
	p.Title.Padding = vg.Length(5)

	p.Y.Min = 0
	p.Y.Label.Text = "Count"
	p.Y.Label.Padding = vg.Length(5)

	// We only deal with whole numbers so undo any use of strconv.FormatFloat.
	origYMarker := p.Y.Tick.Marker
	p.Y.Tick.Marker = plot.TickerFunc(func(min, max float64) []plot.Tick {
		ticks := origYMarker.Ticks(min, max)
		for i := range ticks {
			if ticks[i].Label == "" {
				continue
			}
			ticks[i].Label = strconv.Itoa(int(ticks[i].Value))
		}
		return ticks
	})
	p.Y.Tick.Marker = plot.TickerFunc(thousandTicker(p.Y.Tick.Marker))

	p.Legend.Top = true

	values := make(plotter.Values, 0, len(trvs))
	xLabels := make([]string, 0, len(trvs))

	for _, trv := range trvs {
		values = append(values, float64(trv.val))
		xLabels = append(xLabels, labeler(trv.tr))
	}

	bar, err := plotter.NewBarChart(values, vg.Points(40))
	if err != nil {
		return nil, err
	}
	p.Add(bar)
	p.NominalX(xLabels...)

	wt, err := p.WriterTo(20*vg.Centimeter, 10*vg.Centimeter, "png")
	if err != nil {
		return nil, err
	}

	var b bytes.Buffer
	if _, err := wt.WriteTo(&b); err != nil {
		return nil, err
	}

	if err := padImage(&b); err != nil {
		return nil, err
	}

	return b.Bytes(), nil
}

func thousandTicker(t plot.Ticker) func(min, max float64) []plot.Tick {
	return func(min, max float64) []plot.Tick {
		tt := t.Ticks(min, max)
		for i := range tt {
			if tt[i].Label == "" || tt[i].Value < 1000 || int(tt[i].Value)%1000 != 0 {
				continue
			}
			tt[i].Label = fmt.Sprintf("%dk", int(tt[i].Value/1000))
		}
		return tt
	}
}

type counterSeries struct {
	counter directory.Counter
	series  []timeRangeValue
}

type timeRangeQuerier interface {
	queryCounterSeries(ctx context.Context, counters []directory.Counter, trs []timeRange) ([]counterSeries, error)
}

type counterbaseTimeRangeQuerier struct {
	querier Querier
}

func (q counterbaseTimeRangeQuerier) queryCounterSeries(ctx context.Context, counters []directory.Counter, trs []timeRange) ([]counterSeries, error) {
	var out []counterSeries

	for _, c := range counters {
		trvs, err := q.query(ctx, c.ID, trs)
		if err != nil {
			return nil, err
		}
		if len(trvs) == 0 {
			continue
		}
		if trvSum(trvs) == 0 {
			continue
		}

		out = append(out, counterSeries{counter: c, series: trvs})
	}

	return out, nil
}

func (q counterbaseTimeRangeQuerier) query(ctx context.Context, counterID string, trs []timeRange) ([]timeRangeValue, error) {
	var whens []string
	for _, tr := range trs {
		when := fmt.Sprintf("when time >= %d and time < %d then %d", tr.begin.Unix(), tr.end.Unix(), tr.begin.Unix())
		whens = append(whens, when)
	}
	caseWhen := "case " + strings.Join(whens, " ") + " end"

	qq := fmt.Sprintf("select %s as time, sum(value) from counter_data where counter_id='%s' and time >= %d and time < %d group by 1", caseWhen, counterID, trs[0].begin.Unix(), trs[len(trs)-1].end.Unix())
	pts, err := q.querier.Query(ctx, qq)
	if err != nil {
		return nil, err
	}

	if len(pts) == 0 {
		return nil, err
	}

	var trvs []timeRangeValue
	for _, tr := range trs {
		trv := timeRangeValue{tr: tr}
		for _, p := range pts {
			if p.Time.Equal(tr.begin) {
				trv.val = int(p.Value)
				break
			}
		}
		trvs = append(trvs, trv)
	}

	return trvs, nil
}

type cyclingCounterDirectory interface {
	counters(ctx context.Context, inService timeRange) ([]directory.Counter, error)
}

type cyclingeCounterDirectoryWrapper struct {
	dir Directory
}

func (d cyclingeCounterDirectoryWrapper) counters(ctx context.Context, inService timeRange) ([]directory.Counter, error) {
	counters, err := d.dir.Counters(ctx)
	if err != nil {
		return nil, err
	}
	var cyclingCounters []directory.Counter
	for _, c := range counters {
		if c.Mode != "cycling" {
			continue
		}

		for _, sr := range c.ServiceRanges {
			//     |---|
			// |--|
			// did this range end before inService began
			// is sr.End < inService.begin?
			if !sr.End.IsZero() && sr.End.Before(inService.begin) {
				continue
			}

			//     |---|
			//          |--|
			// did this range start after inService ended?
			// is sr.Begin >= inService.end?
			// is inService.end < sr.Start?
			if !inService.end.IsZero() && inService.end.Before(sr.Start.Time) {
				continue
			}

			cyclingCounters = append(cyclingCounters, c)
			break
		}
	}

	return cyclingCounters, nil
}

func padImage(b *bytes.Buffer) error {
	img, _, err := image.Decode(b)
	if err != nil {
		return err
	}

	bnds := img.Bounds()
	const padding = 20
	outRect := image.Rect(bnds.Min.X-padding, bnds.Min.Y-padding, bnds.Max.X+padding, bnds.Max.Y+padding)
	out := image.NewRGBA(outRect)
	draw.Draw(out, out.Bounds(), &image.Uniform{color.RGBA{255, 255, 255, 255}}, image.Point{}, draw.Src)
	draw.Draw(out, img.Bounds(), img, outRect.Min.Add(image.Pt(padding, padding)), draw.Over)

	b.Reset()
	return png.Encode(b, out)
}

func trvSum(trvs []timeRangeValue) int {
	var out int
	for _, trv := range trvs {
		out += trv.val
	}
	return out
}
