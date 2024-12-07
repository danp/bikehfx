package main

import (
	"bytes"
	"cmp"
	"context"
	"flag"
	"fmt"
	"hash/crc32"
	"image/color"
	"log"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danp/counterbase/directory"
	"github.com/graxinc/errutil"
	"github.com/peterbourgon/ff/v3/ffcli"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/plotutil"
	"gonum.org/v1/plot/vg"
)

func newDailyCmd(rootConfig *rootConfig) *ffcli.Command {
	var (
		fs   = flag.NewFlagSet("bikehfx-tweet daily", flag.ExitOnError)
		day  = fs.String("day", time.Now().AddDate(0, 0, -1).Format("20060102"), "day to post for, in YYYYMMDD form")
		days commaSeparatedString
	)
	fs.Var(&days, "days", "comma-separated days to post, in YYYYMMDD form, preferred over day")

	return &ffcli.Command{
		Name:       "daily",
		ShortUsage: "bikehfx-tweet daily",
		ShortHelp:  "send daily post",
		FlagSet:    fs,
		Exec: func(ctx context.Context, args []string) error {
			days := days.vals
			if len(days) == 0 {
				days = []string{*day}
			}
			return dailyExec(ctx, days, rootConfig.trq, rootConfig.rc, rootConfig.twt)
		},
	}
}

func dailyExec(ctx context.Context, days []string, trq counterbaseTimeRangeQuerier, rc recordsChecker, twt postThread) error {
	loc, err := time.LoadLocation("America/Halifax")
	if err != nil {
		return errutil.With(err)
	}

	var posts []post
	for _, day := range days {
		dayt, err := time.ParseInLocation("20060102", day, loc)
		if err != nil {
			return errutil.With(err)
		}

		ts, err := dayPost(ctx, dayt, trq, ecWeatherer{}, rc, pngDayGrapher{})
		if err != nil {
			return errutil.With(err)
		}

		posts = append(posts, ts...)
	}

	if _, err := twt.postThread(ctx, posts); err != nil {
		return errutil.With(err)
	}
	return nil
}

type weatherer interface {
	weather(ctx context.Context, day time.Time) (weather, error)
}

type dayGrapher interface {
	graph(ctx context.Context, day time.Time, cs []counterSeries) (_ []byte, altText string, _ error)
}

type counterSeries struct {
	counter     directory.Counter
	last        time.Time
	lastNonZero time.Time
	series      []timeRangeValue
}

func dayPost(ctx context.Context, day time.Time, trq counterbaseTimeRangeQuerier, weatherer weatherer, recordsChecker recordsChecker, grapher dayGrapher) ([]post, error) {
	dayRange := newTimeRangeDate(time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location()), 0, 0, 1)

	cs, err := trq.query(ctx, dayRange)
	if err != nil {
		return nil, errutil.With(err)
	}

	var anyBikes bool
	for _, c := range cs {
		for _, v := range c.series {
			if v.val > 0 {
				anyBikes = true
				break
			}
		}
	}
	if !anyBikes {
		log.Printf("no bikes counted on %v", day)
		return nil, nil
	}

	records, err := recordsChecker.check(ctx, day, cs, recordWidthDay)
	if err != nil {
		return nil, errutil.With(err)
	}

	w, err := weatherer.weather(ctx, day)
	if err != nil {
		log.Printf("weatherer.weather: %v", err)
		w = weather{}
	}

	text := dayPostText(day, w, cs, records)

	dayHours := dayRange.split(time.Hour)
	hourSeries, err := trq.query(ctx, dayHours...)
	if err != nil {
		return nil, errutil.With(err)
	}

	dg, dat, err := grapher.graph(ctx, day, hourSeries)
	if err != nil {
		return nil, errutil.With(err)
	}

	media := []postMedia{{b: dg, altText: dat}}

	return []post{
		{text: text, media: media},
	}, nil
}

func dayPostText(day time.Time, w weather, cs []counterSeries, records map[string]recordKind) string {
	var out strings.Builder

	p := message.NewPrinter(language.English)

	var sum int
	var presentIndices, missingIndices []int
	for i, c := range cs {
		for _, v := range c.series {
			sum += v.val
		}
		if !c.last.Before(day) && !c.lastNonZero.Before(day) {
			presentIndices = append(presentIndices, i)
			continue
		}
		missingIndices = append(missingIndices, i)
	}

	p.Fprintf(&out, "%v%v #BikeHfx bikes counted %v\n\n", sum, recordSymbol(records["sum"]), day.Format("Mon Jan 2"))
	if w.max != 0 {
		p.Fprintf(&out, "%v/%v C", int(math.Ceil(w.max)), int(math.Floor(w.min)))
		if w.rain > 0 {
			raindrop := "\U0001f4a7"
			p.Fprintf(&out, " %v %.1fmm", raindrop, w.rain)
		}
		if w.snow > 0 {
			snowflake := "\u2744\ufe0f"
			p.Fprintf(&out, " %v %.1fcm", snowflake, w.snow)
		}
		p.Fprintf(&out, "\n\n")
	}

	slices.SortFunc(presentIndices, func(i, j int) int {
		return cmp.Compare(counterName(cs[i].counter), counterName(cs[j].counter))
	})
	for _, i := range presentIndices {
		c := cs[i]
		v := c.series[len(c.series)-1].val
		p.Fprintf(&out, "%v%v %v\n", v, recordSymbol(records[c.counter.ID]), counterName(c.counter))
	}

	recordKinds := make(map[recordKind]struct{})
	for _, k := range records {
		recordKinds[k] = struct{}{}
	}
	if len(recordKinds) > 0 {
		p.Fprintln(&out)
		for _, k := range slices.Sorted(maps.Keys(recordKinds)) {
			p.Fprintln(&out, recordNote(k))
		}
	}

	if len(missingIndices) > 0 {
		slices.SortFunc(missingIndices, func(i, j int) int {
			return cmp.Compare(counterName(cs[i].counter), counterName(cs[j].counter))
		})

		p.Fprintln(&out)
		p.Fprintln(&out, "Missing (last):")
		for _, i := range missingIndices {
			c := cs[i]
			last := c.last
			if !c.lastNonZero.IsZero() {
				last = c.lastNonZero
			}
			p.Fprintf(&out, "%v (%v)\n", counterName(c.counter), last.Format("Jan 2"))
		}
	}

	return strings.TrimSpace(out.String())
}

type pngDayGrapher struct{}

func (pngDayGrapher) graph(ctx context.Context, day time.Time, cs []counterSeries) ([]byte, string, error) {
	counterXYs := make(map[string]plotter.XYs)

	earliestNonZeroHour := 24
	for _, s := range cs {
		xys := make(plotter.XYs, 24)
		for _, trv := range s.series {
			hour := trv.tr.begin.Hour()
			xys[hour].X = float64(hour)
			xys[hour].Y = float64(trv.val)
		}

		for i, xy := range xys {
			if xy.Y == 0 {
				continue
			}
			if i < earliestNonZeroHour {
				earliestNonZeroHour = i
				break
			}
		}

		counterXYs[s.counter.ID] = xys
	}

	// ---

	if err := initGraph(); err != nil {
		return nil, "", errutil.With(err)
	}

	p := plot.New()

	p.Title.Text = "Counts for " + day.Format("Mon Jan 2") + " by hour starting"

	p.X.Tick.Marker = hourTicker{}

	p.Y.Min = 0
	p.Y.Label.Text = "Count"

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

	p.Legend.Top = true
	p.Legend.Left = true

	grid := plotter.NewGrid()
	grid.Vertical.Color = color.Gray{175}
	grid.Horizontal.Color = color.Gray{175}
	p.Add(grid)

	var seriesIndices []int
	for i := range cs {
		seriesIndices = append(seriesIndices, i)
	}
	slices.SortFunc(seriesIndices, func(i, j int) int {
		return cmp.Compare(counterName(cs[i].counter), counterName(cs[j].counter))
	})

	type colorDash struct {
		colorIdx int
		dashIdx  int
	}
	colorDashes := make(map[colorDash]struct{})

	for _, si := range seriesIndices {
		s := cs[si]
		c := s.counter
		xys := counterXYs[c.ID]

		ln, err := plotter.NewLine(xys[earliestNonZeroHour:])
		if err != nil {
			return nil, "", errutil.With(err)
		}

		ci := crc32.ChecksumIEEE([]byte(c.Name)) // using full name

		colorIdx := int(ci) % len(plotutil.DefaultColors)
		dashIdx := int(ci) % len(plotutil.DefaultDashes)
		var changedDashes bool
		for {
			if _, ok := colorDashes[colorDash{colorIdx, dashIdx}]; !ok {
				colorDashes[colorDash{colorIdx, dashIdx}] = struct{}{}
				break
			}
			if changedDashes {
				colorIdx++
				changedDashes = false
				continue
			}
			dashIdx++
			changedDashes = true
		}

		ln.LineStyle.Color = plotutil.DefaultColors[colorIdx]
		ln.LineStyle.Dashes = plotutil.DefaultDashes[dashIdx]

		ln.LineStyle.Width = vg.Points(2)

		p.Add(ln)
		p.Legend.Add(counterName(c), ln)
	}

	wt, err := p.WriterTo(20*vg.Centimeter, 10*vg.Centimeter, "png")
	if err != nil {
		return nil, "", errutil.With(err)
	}

	var b bytes.Buffer
	if _, err := wt.WriteTo(&b); err != nil {
		return nil, "", errutil.With(err)
	}

	if err := padImage(&b); err != nil {
		return nil, "", errutil.With(err)
	}

	return b.Bytes(), dailyAltText(cs), nil
}

func dailyAltText(cs []counterSeries) string {
	if len(cs) == 0 {
		return ""
	}

	hhs := []counterSeries{{series: []timeRangeValue{{}}}}

	var counterNames []string
	for _, c := range cs {
		counterNames = append(counterNames, c.counter.Name) // using full name
		for _, trv := range c.series {
			fv := hhs[0].series[0].val

			if trv.val > fv {
				hhs = []counterSeries{{counter: c.counter, series: []timeRangeValue{trv}}}
			} else if trv.val == fv {
				hhs = append(hhs, counterSeries{counter: c.counter, series: []timeRangeValue{trv}})
			}
		}
	}
	slices.Sort(counterNames)

	if hhs[0].series[0].val == 0 {
		return ""
	}

	counters := "counters"
	if len(counterNames) < 2 {
		counters = "counter"
	}
	out := fmt.Sprintf("Line chart of bikes counted by hour from the %s %s.", humanList(counterNames), counters)

	if len(hhs) == 1 {
		hh := hhs[0]
		hf := hh.series[0].tr.begin.Format("3 PM")
		// using full name
		out += fmt.Sprintf(" The highest hourly count was %d during the %s hour from the %s counter.", hh.series[0].val, hf, hh.counter.Name)
	} else if len(hhs) > 1 {
		hcn := make([]string, 0, len(hhs))
		seen := make(map[string]bool)
		for _, hh := range hhs {
			// using full name
			if !seen[hh.counter.Name] {
				hcn = append(hcn, hh.counter.Name)
				seen[hh.counter.Name] = true
			}
		}
		slices.Sort(hcn)
		counter := "counter"
		if len(hcn) > 1 {
			counter += "s"
		}
		out += fmt.Sprintf(" The highest hourly count was %d from the %s %s.", hhs[0].series[0].val, humanList(hcn), counter)
	}
	return out
}

// adapted from https://github.com/dustin/go-humanize/blob/master/english/words.go
func humanList(words []string) string {
	const joiner = " and "
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	case 2:
		return strings.Join(words, joiner)
	default:
		return strings.Join(words[:len(words)-1], ", ") + "," + joiner + words[len(words)-1]
	}
}

type hourTicker struct{}

func (h hourTicker) Ticks(min, max float64) []plot.Tick {
	var ts []plot.Tick

	for i := 0; i < 24; i++ {
		t := plot.Tick{
			Value: float64(i),
		}
		if i%2 == 0 {
			var tt time.Time
			tt = tt.Add(time.Duration(i) * time.Hour)
			t.Label = tt.Format("3PM")
		}
		ts = append(ts, t)
	}

	return ts
}
