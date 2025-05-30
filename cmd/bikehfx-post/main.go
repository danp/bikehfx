package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"maps"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/danp/counterbase/directory"
	"github.com/danp/counterbase/query"
	"github.com/graxinc/errutil"
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

	rootCfg.ccd = cyclingCounterDirectoryWrapper{dir: dir}

	qu := &query.Client{
		URL: rootCfg.queryURL,
	}

	rootCfg.trq = counterbaseTimeRangeQuerier{rootCfg.ccd, qu}

	rootCfg.rc = counterbaseRecordser{
		qu:  qu,
		ccd: rootCfg.ccd,
	}

	var tp threadPoster
	if rootCfg.testMode {
		tp = posterThreader{p: &savePoster{}, initial: rootCfg.initialPost}
	} else {
		var mtt multiPosterThreader

		if rootCfg.mastodonClientID != "" {
			mt, err := newMastodonTooter(rootCfg.mastodonServer, rootCfg.mastodonClientID, rootCfg.mastodonClientSecret, rootCfg.mastodonAccessToken)
			if err != nil {
				log.Println(err)
			} else {
				mtt = append(mtt, posterThreader{p: mt, inReplyTo: rootCfg.mastodonInReplyTo, initial: rootCfg.initialPost})
			}
		}

		if rootCfg.bskyHandle != "" {
			bt, err := newBlueskyPoster(rootCfg.bskyServer, rootCfg.bskyHandle, rootCfg.bskyPassword)
			if err != nil {
				log.Println(err)
			} else {
				mtt = append(mtt, posterThreader{p: bt, inReplyTo: rootCfg.bskyInReplyTo, initial: rootCfg.initialPost})
			}
		}

		if len(mtt) == 0 {
			log.Fatal("no post threaders configured")
		}

		tp = mtt
	}

	rootCfg.tp = tp

	if err := rootCmd.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

type rootConfig struct {
	directoryURL string
	queryURL     string

	initialPost string

	mastodonServer       string
	mastodonClientID     string
	mastodonClientSecret string
	mastodonAccessToken  string
	mastodonInReplyTo    string

	bskyServer    string
	bskyHandle    string
	bskyPassword  string
	bskyInReplyTo string

	testMode bool

	ccd cyclingCounterDirectory
	trq counterbaseTimeRangeQuerier
	rc  recordser
	tp  threadPoster
}

func newRootCmd() (*ffcli.Command, *rootConfig) {
	var cfg rootConfig

	fs := flag.NewFlagSet("bikehfx-post", flag.ExitOnError)

	fs.StringVar(&cfg.directoryURL, "directory-url", "", "directory URL")
	fs.StringVar(&cfg.queryURL, "query-url", "", "query URL")

	fs.StringVar(&cfg.initialPost, "initial-post", "", "if set, text for first post")

	fs.StringVar(&cfg.mastodonServer, "mastodon-server", "", "mastodon server URL")
	// https://docs.joinmastodon.org/client/token/, requires read:accounts, write:media, write:statuses
	fs.StringVar(&cfg.mastodonClientID, "mastodon-client-id", "", "mastodon client id/key")
	fs.StringVar(&cfg.mastodonClientSecret, "mastodon-client-secret", "", "mastodon client secret")
	fs.StringVar(&cfg.mastodonAccessToken, "mastodon-access-token", "", "mastodon access token")
	fs.StringVar(&cfg.mastodonInReplyTo, "mastodon-in-reply-to", "", "if set, first post will reply to this status id")

	fs.StringVar(&cfg.bskyServer, "bsky-server", "https://bsky.social", "bluesky server URL")
	fs.StringVar(&cfg.bskyHandle, "bsky-handle", "", "bluesky handle")
	fs.StringVar(&cfg.bskyPassword, "bsky-password", "", "bluesky password")
	fs.StringVar(&cfg.bskyInReplyTo, "bsky-in-reply-to", "", "if set, first post will reply to this status at proto URI or web URL")

	fs.BoolVar(&cfg.testMode, "test-mode", false, "if enabled, write generated posts to disk instead of posting")

	return &ffcli.Command{
		ShortUsage: "bikehfx-post [flags] <subcommand>",
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
		return nil, errutil.With(err)
	}

	switch u.Scheme {
	case "file":
		src = u.Path

		f, err := os.Open(src)
		if err != nil {
			return nil, errutil.With(err)
		}
		defer f.Close()

		if err := json.NewDecoder(f).Decode(&counters); err != nil {
			return nil, errutil.With(err)
		}
	case "http", "https":
		resp, err := http.Get(src) //nolint:gosec
		if err != nil {
			return nil, errutil.With(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			return nil, errutil.New(errutil.Tags{"code": resp.StatusCode})
		}

		if err := json.NewDecoder(resp.Body).Decode(&counters); err != nil {
			return nil, errutil.With(err)
		}
	default:
		return nil, errutil.New(errutil.Tags{"scheme": u.Scheme})
	}

	return staticDirectory{C: counters}, nil
}

type threadPoster interface {
	postThread(context.Context, []post) ([]string, error)
}

type poster interface {
	post(context.Context, post) (string, error)
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
		plotutil.DefaultColors = plotutil.DarkColors
	})
	return initGraphErr
}

type staticDirectory struct {
	C []directory.Counter
}

func (f staticDirectory) Counters(ctx context.Context) ([]directory.Counter, error) {
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

func (r timeRange) String() string {
	return fmt.Sprintf("[%s, %s)", r.begin.Format(time.RFC3339), r.end.Format(time.RFC3339))
}

func (r timeRange) addDate(years, months, days int) timeRange {
	return timeRange{begin: r.begin.AddDate(years, months, days), end: r.end.AddDate(years, months, days)}
}

func (r timeRange) startOfWeek() timeRange {
	r.begin = r.begin.AddDate(0, 0, -int(r.begin.Weekday()))
	r.end = r.begin.AddDate(0, 0, 7)
	return r
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
		return nil, errutil.With(err)
	}

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
		return nil, errutil.With(err)
	}
	p.Add(bar)
	p.NominalX(xLabels...)

	wt, err := p.WriterTo(20*vg.Centimeter, 10*vg.Centimeter, "png")
	if err != nil {
		return nil, errutil.With(err)
	}

	var b bytes.Buffer
	if _, err := wt.WriteTo(&b); err != nil {
		return nil, errutil.With(err)
	}

	if err := padImage(&b); err != nil {
		return nil, errutil.With(err)
	}

	return b.Bytes(), nil
}

func yearWeekChart(trvs map[int]map[int]timeRangeValue, title string) ([]byte, error) {
	if err := initGraph(); err != nil {
		return nil, errutil.With(err)
	}

	p := plot.New()

	p.Title.Text = title
	p.Title.Padding = vg.Length(5)

	p.Y.Min = 0
	p.Y.Label.Text = "Count"
	p.Y.Label.Padding = vg.Length(5)

	p.X.Label.Text = "Week"

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

	years := slices.Sorted(maps.Keys(trvs))

	thisYear := years[len(years)-1]

	// use this year's week end dates as the x-axis labels
	p.X.Tick.Marker = plot.TickerFunc(func(min, max float64) []plot.Tick {
		var ticks []plot.Tick
		var lastTickMonth time.Month
		for week := 1; week <= 53; week++ {
			weekEnd, err := isoYearWeekToDate(thisYear, week)
			if err != nil {
				return nil
			}
			t := plot.Tick{Value: float64(week)}
			if lastTickMonth != weekEnd.Month() {
				t.Label = weekEnd.Format("Jan")
			}
			lastTickMonth = weekEnd.Month()
			ticks = append(ticks, t)
		}
		return ticks
	})

	p.Legend.Top = true

	for _, year := range years {
		weeks := trvs[year]
		var pts plotter.XYs
		for week := 1; week <= 53; week++ {
			trv, ok := weeks[week]
			if !ok {
				continue
			}
			pts = append(pts, plotter.XY{X: float64(week), Y: float64(trv.val)})
		}

		ln, err := plotter.NewLine(pts)
		if err != nil {
			return nil, errutil.With(err)
		}

		ln.LineStyle.Color = plotutil.Color(year)
		ln.LineStyle.Dashes = plotutil.Dashes(year)

		ln.LineStyle.Width = vg.Points(2)

		p.Add(ln)

		p.Add(ln)
		p.Legend.Add(fmt.Sprint(year), ln)
	}

	wt, err := p.WriterTo(20*vg.Centimeter, 10*vg.Centimeter, "png")
	if err != nil {
		return nil, errutil.With(err)
	}

	var b bytes.Buffer
	if _, err := wt.WriteTo(&b); err != nil {
		return nil, errutil.With(err)
	}

	if err := padImage(&b); err != nil {
		return nil, errutil.With(err)
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

type counterbaseTimeRangeQuerier struct {
	ccd     cyclingCounterDirectory
	querier Querier
}

func (q counterbaseTimeRangeQuerier) query(ctx context.Context, trs ...timeRange) ([]counterSeries, error) {
	counters, err := q.ccd.counters(ctx, timeRange{trs[0].begin, trs[len(trs)-1].end})
	if err != nil {
		return nil, errutil.With(err)
	}

	cs, err := q.queryCounterSeries(ctx, counters, trs)
	if err != nil {
		return nil, errutil.With(err)
	}

	for _, counter := range counters {
		last, lastNonZero, err := q.last(ctx, counter.ID, trs[len(trs)-1].end)
		if err != nil {
			return nil, errutil.With(err)
		}
		var found bool
		for i, s := range cs {
			if s.counter.ID != counter.ID {
				continue
			}
			found = true
			cs[i].last = last
			cs[i].lastNonZero = lastNonZero
		}
		if !found {
			cs = append(cs, counterSeries{counter: counter, last: last, lastNonZero: lastNonZero})
		}
	}
	return cs, nil
}

func (q counterbaseTimeRangeQuerier) queryCounterSeries(ctx context.Context, counters []directory.Counter, trs []timeRange) ([]counterSeries, error) {
	var out []counterSeries

	for _, c := range counters {
		trvs, err := q.timeRangeValues(ctx, c.ID, trs)
		if err != nil {
			return nil, errutil.With(err)
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

func (q counterbaseTimeRangeQuerier) timeRangeValues(ctx context.Context, counterID string, trs []timeRange) ([]timeRangeValue, error) {
	var whens []string
	for _, tr := range trs {
		when := fmt.Sprintf("when time >= %d and time < %d then %d", tr.begin.Unix(), tr.end.Unix(), tr.begin.Unix())
		whens = append(whens, when)
	}
	caseWhen := "case " + strings.Join(whens, " ") + " end"

	qq := fmt.Sprintf("select %s as time, sum(value) from counter_data where counter_id='%s' and time >= %d and time < %d group by 1", caseWhen, counterID, trs[0].begin.Unix(), trs[len(trs)-1].end.Unix())
	pts, err := q.querier.Query(ctx, qq)
	if err != nil {
		return nil, errutil.With(err)
	}

	if len(pts) == 0 {
		return nil, nil
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

func (q counterbaseTimeRangeQuerier) last(ctx context.Context, counterID string, until time.Time) (all, nonZero time.Time, _ error) {
	qq := fmt.Sprintf("select max(time) as time, 1 from counter_data where counter_id='%s' and time <= %v", counterID, until.Unix())
	pts, err := q.querier.Query(ctx, qq)
	if err != nil {
		return time.Time{}, time.Time{}, errutil.With(err)
	}
	if len(pts) == 0 {
		return time.Time{}, time.Time{}, nil
	}
	if len(pts) > 1 {
		return time.Time{}, time.Time{}, errutil.New(errutil.Tags{"points": len(pts)})
	}
	all = pts[0].Time
	qq = fmt.Sprintf("select max(time) as time, 1 from counter_data where counter_id='%s' and time <= %v and value > 0", counterID, until.Unix())
	pts, err = q.querier.Query(ctx, qq)
	if err != nil {
		return time.Time{}, time.Time{}, errutil.With(err)
	}
	if len(pts) == 0 {
		return all, time.Time{}, nil
	}
	if len(pts) > 1 {
		return time.Time{}, time.Time{}, errutil.New(errutil.Tags{"points": len(pts)})
	}
	return all, pts[0].Time, nil
}

type cyclingCounterDirectory interface {
	counters(ctx context.Context, inService timeRange) ([]directory.Counter, error)
}

type cyclingCounterDirectoryWrapper struct {
	dir Directory
}

func (d cyclingCounterDirectoryWrapper) counters(ctx context.Context, inService timeRange) ([]directory.Counter, error) {
	counters, err := d.dir.Counters(ctx)
	if err != nil {
		return nil, errutil.With(err)
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
		return errutil.With(err)
	}

	bnds := img.Bounds()
	const padding = 20
	outRect := image.Rect(bnds.Min.X-padding, bnds.Min.Y-padding, bnds.Max.X+padding, bnds.Max.Y+padding)
	out := image.NewRGBA(outRect)
	draw.Draw(out, out.Bounds(), &image.Uniform{color.RGBA{255, 255, 255, 255}}, image.Point{}, draw.Src)
	draw.Draw(out, img.Bounds(), img, outRect.Min.Add(image.Pt(padding, padding)), draw.Over)

	b.Reset()
	if err := png.Encode(b, out); err != nil {
		return errutil.With(err)
	}
	return nil
}

func trvSum(trvs []timeRangeValue) int {
	var out int
	for _, trv := range trvs {
		out += trv.val
	}
	return out
}

func counterName(c directory.Counter) string {
	if c.ShortName != "" {
		return c.ShortName
	}
	return c.Name
}

// isoYearWeekToDate converts a year and ISO week number to the date of the last day of the week (Saturday).
func isoYearWeekToDate(year int, week int) (time.Time, error) {
	if week < 1 || week > 53 {
		return time.Time{}, fmt.Errorf("week number must be between 1 and 53")
	}

	// Start with the first day of the year
	startOfYear := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)

	// Find the ISO week day of the first day of the year (1 = Monday, 7 = Sunday)
	isoWeekDay := startOfYear.Weekday()
	if isoWeekDay == time.Sunday {
		isoWeekDay = 7
	} else {
		isoWeekDay -= 1
	}

	// Calculate days to the first Thursday (start of the first ISO week)
	daysToFirstThursday := (3 - int(isoWeekDay) + 7) % 7

	// Calculate the start of the target ISO week
	daysToWeek := daysToFirstThursday + (week-1)*7

	// Adjust to get to the last day of the week (Saturday)
	daysToWeek += 2

	// Adjust to get to the last day of the week (Saturday)
	firstDayOfWeek := startOfYear.AddDate(0, 0, daysToWeek)

	return firstDayOfWeek, nil
}

//go:embed scripts/*
var scripts embed.FS

func runUVScript(ctx context.Context, script string, input any) ([]byte, error) {
	b, err := json.Marshal(input)
	if err != nil {
		return nil, errutil.With(err)
	}

	td, err := os.MkdirTemp("", "script")
	if err != nil {
		return nil, errutil.With(err)
	}
	defer os.RemoveAll(td)

	scriptBytes, err := scripts.ReadFile(filepath.Join("scripts", script))
	if err != nil {
		return nil, errutil.With(err)
	}
	scriptPath := filepath.Join(td, "script.py")
	if err := os.WriteFile(scriptPath, scriptBytes, 0600); err != nil {
		return nil, errutil.With(err)
	}
	if err := os.Chmod(scriptPath, 0755); err != nil {
		return nil, errutil.With(err)
	}
	scriptLockBytes, err := scripts.ReadFile(filepath.Join("scripts", script+".lock"))
	if err != nil {
		return nil, errutil.With(err)
	}
	scriptLockPath := filepath.Join(td, "script.py.lock")
	if err := os.WriteFile(scriptLockPath, scriptLockBytes, 0600); err != nil {
		return nil, errutil.With(err)
	}

	var out bytes.Buffer

	cmd := exec.CommandContext(ctx, "uv", "run", "--quiet", "--frozen", scriptPath) //nolint:gosec
	cmd.Stdin = bytes.NewReader(b)
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return nil, errutil.With(err)
	}

	return out.Bytes(), nil
}
