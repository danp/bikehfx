package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"hash/crc32"
	"image/color"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/peterbourgon/ff/v3/ffcli"
	"golang.org/x/text/message"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/plotutil"
	"gonum.org/v1/plot/vg"
)

func newDailyCmd(rootConfig *rootConfig) *ffcli.Command {
	var (
		fs   = flag.NewFlagSet("bikehfx-tweet daily", flag.ExitOnError)
		day  = fs.String("day", time.Now().AddDate(0, 0, -1).Format("20060102"), "day to tweet for, in YYYYMMDD form")
		days commaSeparatedString
	)
	fs.Var(&days, "days", "comma-separated days to tweet, in YYYYMMDD form, preferred over day")

	return &ffcli.Command{
		Name:       "daily",
		ShortUsage: "bikehfx-tweet daily",
		ShortHelp:  "send daily tweet",
		FlagSet:    fs,
		Exec: func(ctx context.Context, args []string) error {
			days := days.vals
			if len(days) == 0 {
				days = []string{*day}
			}

			return dailyExec(ctx, days, rootConfig.ccd, rootConfig.trq, rootConfig.rc, rootConfig.twt)
		},
	}
}

func dailyExec(ctx context.Context, days []string, ccd cyclingCounterDirectory, trq timeRangeQuerier, rc recordsChecker, twt tweetThread) error {
	loc, err := time.LoadLocation("America/Halifax")
	if err != nil {
		return err
	}

	var tweets []tweet
	for _, day := range days {
		dayt, err := time.Parse("20060102", day)
		if err != nil {
			return err
		}
		dayRange := newTimeRangeDate(time.Date(dayt.Year(), dayt.Month(), dayt.Day(), 0, 0, 0, 0, loc), 0, 0, 1)

		counters, err := ccd.counters(ctx, dayRange)
		if err != nil {
			return err
		}

		daySeries, err := trq.queryCounterSeries(ctx, counters, []timeRange{dayRange})
		if err != nil {
			return err
		}
		if len(daySeries) == 0 {
			continue
		}

		records, err := rc.check(ctx, dayRange.begin, daySeries, recordWidthDay)
		if err != nil {
			return err
		}

		dt := tweetText(daySeries, records, func(p *message.Printer, sum string) string {
			return p.Sprintf("%s #bikehfx trips counted %s", sum, dayRange.begin.Format("Mon Jan 2"))
		})

		dayHours := dayRange.split(time.Hour)

		hourSeries, err := trq.queryCounterSeries(ctx, counters, dayHours)
		if err != nil {
			return err
		}

		dg, err := dailyGraph(dayRange.begin, hourSeries)
		if err != nil {
			return err
		}

		dat := dailyAltText(hourSeries)

		media := []tweetMedia{{b: dg, altText: dat}}

		tweets = append(tweets, tweet{
			text:  dt,
			media: media,
		})
	}

	_, err = twt.tweetThread(ctx, tweets)
	return err
}

func dailyGraph(day time.Time, cs []counterSeries) ([]byte, error) {
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

		counterXYs[s.counter.Name] = xys
	}

	// ---

	if err := initGraph(); err != nil {
		return nil, err
	}
	plotutil.DefaultColors = plotutil.DarkColors

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
	sort.Slice(seriesIndices, func(i, j int) bool {
		return cs[seriesIndices[i]].counter.Name < cs[seriesIndices[j]].counter.Name
	})

	for _, si := range seriesIndices {
		s := cs[si]
		cn := s.counter.Name
		xys := counterXYs[cn]

		ln, err := plotter.NewLine(xys[earliestNonZeroHour:])
		if err != nil {
			return nil, err
		}

		ci := crc32.ChecksumIEEE([]byte(cn))

		ln.LineStyle.Color = plotutil.Color(int(ci))
		ln.LineStyle.Dashes = plotutil.Dashes(int(ci))

		ln.LineStyle.Width = vg.Points(2)

		p.Add(ln)
		p.Legend.Add(cn, ln)
	}

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

func dailyAltText(cs []counterSeries) string {
	if len(cs) == 0 {
		return ""
	}

	hhs := []counterSeries{{series: []timeRangeValue{{}}}}

	var counterNames []string
	for _, c := range cs {
		counterNames = append(counterNames, c.counter.Name)
		for _, trv := range c.series {
			fv := hhs[0].series[0].val

			if trv.val > fv {
				hhs = []counterSeries{{counter: c.counter, series: []timeRangeValue{trv}}}
			} else if trv.val == fv {
				hhs = append(hhs, counterSeries{counter: c.counter, series: []timeRangeValue{trv}})
			}
		}
	}
	sort.Strings(counterNames)

	if hhs[0].series[0].val == 0 {
		return ""
	}

	counters := "counters"
	if len(counterNames) < 2 {
		counters = "counter"
	}
	out := fmt.Sprintf("Line chart of bike trips by hour from the %s %s.", humanList(counterNames), counters)

	if len(hhs) == 1 {
		hh := hhs[0]
		hf := hh.series[0].tr.begin.Format("3 PM")
		out += fmt.Sprintf(" The highest hourly count was %d during the %s hour from the %s counter.", hh.series[0].val, hf, hh.counter.Name)
	} else if len(hhs) > 1 {
		hcn := make([]string, 0, len(hhs))
		seen := make(map[string]bool)
		for _, hh := range hhs {
			if !seen[hh.counter.Name] {
				hcn = append(hcn, hh.counter.Name)
				seen[hh.counter.Name] = true
			}
		}
		sort.Strings(hcn)
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
