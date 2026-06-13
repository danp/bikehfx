package main

import (
	"cmp"
	"context"
	_ "embed"
	"flag"
	"fmt"
	"log"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/danp/counterbase/directory"
	"github.com/graxinc/errutil"
	"github.com/peterbourgon/ff/v3/ffcli"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

func newDailyCmd(rootConfig *rootConfig) *ffcli.Command {
	var (
		fs   = flag.NewFlagSet("bikehfx-post daily", flag.ExitOnError)
		day  = fs.String("day", time.Now().AddDate(0, 0, -1).Format("20060102"), "day to post for, in YYYYMMDD form")
		days commaSeparatedString
	)
	fs.Var(&days, "days", "comma-separated days to post, in YYYYMMDD form, preferred over day")

	return &ffcli.Command{
		Name:       "daily",
		ShortUsage: "bikehfx-post daily",
		ShortHelp:  "send daily post",
		FlagSet:    fs,
		Exec: func(ctx context.Context, args []string) error {
			days := days.vals
			if len(days) == 0 {
				days = []string{*day}
			}
			return dailyExec(ctx, days, rootConfig.trq, rootConfig.rc, rootConfig.tp)
		},
	}
}

func dailyExec(ctx context.Context, days []string, trq counterbaseTimeRangeQuerier, rc recordser, tp threadPoster) error {
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

		ps, err := dayPost(ctx, dayt, trq, ecWeatherer{}, rc, uvScriptHeatmaper{})
		if err != nil {
			return errutil.With(err)
		}

		posts = append(posts, ps...)
	}

	if _, err := tp.postThread(ctx, posts); err != nil {
		return errutil.With(err)
	}
	return nil
}

type weatherer interface {
	weather(ctx context.Context, day time.Time) (weather, error)
}

type dayHeatmaper interface {
	heatmap(ctx context.Context, day time.Time, cs []counterSeries) (_ []byte, altText string, _ error)
}

type counterSeries struct {
	counter     directory.Counter
	last        time.Time
	lastNonZero time.Time
	status      counterDataStatus
	series      []timeRangeValue
}

func dayPost(ctx context.Context, day time.Time, trq counterbaseTimeRangeQuerier, weatherer weatherer, recordser recordser, heatmaper dayHeatmaper) ([]post, error) {
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

	records, err := recordser.records(ctx, day, cs, recordWidthDay)
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

	dg, dat, err := heatmaper.heatmap(ctx, day, hourSeries)
	if err != nil {
		return nil, errutil.With(err)
	}

	media := []postMedia{{
		b:       dg,
		altText: dat,
	}}

	posts := []post{{text: text, media: media}}
	if statusPostText := counterStatusPostText(day, cs); statusPostText != "" {
		posts = append(posts, post{text: statusPostText})
	}
	return posts, nil
}

func dayPostText(day time.Time, w weather, cs []counterSeries, records map[string]recordKind) string {
	var out strings.Builder

	p := message.NewPrinter(language.English)

	var sum int
	var presentIndices []int
	for i, c := range cs {
		for _, v := range c.series {
			sum += v.val
		}
		if len(c.series) > 0 && c.status != counterDataStatusMissing {
			presentIndices = append(presentIndices, i)
		}
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
		p.Fprintf(&out, "%v%v%v %v\n", v, recordSymbol(records[c.counter.ID]), counterStatusSymbol(c.status), counterName(c.counter))
	}

	appendPostMarkerNotes(&out, records, cs)

	return strings.TrimSpace(out.String())
}

func counterStatusSymbol(status counterDataStatus) string {
	if status == counterDataStatusPartial {
		return "!"
	}
	return ""
}

func appendPostMarkerNotes(out *strings.Builder, records map[string]recordKind, cs []counterSeries) {
	recordKinds := make(map[recordKind]struct{})
	for _, k := range records {
		recordKinds[k] = struct{}{}
	}
	hasPartialData := hasPartialCounterData(cs)
	if len(recordKinds) == 0 && !hasPartialData {
		return
	}
	if out.Len() > 0 {
		out.WriteString("\n")
	}
	p := message.NewPrinter(language.English)
	for _, k := range slices.Sorted(maps.Keys(recordKinds)) {
		p.Fprintln(out, recordNote(k))
	}
	if hasPartialData {
		p.Fprintln(out, "! partial data")
	}
}

func hasPartialCounterData(cs []counterSeries) bool {
	for _, c := range cs {
		if c.status == counterDataStatusPartial {
			return true
		}
	}
	return false
}

func counterStatusPostText(asOf time.Time, cs []counterSeries) string {
	var partial, missing []counterSeries
	for _, c := range cs {
		switch c.status {
		case counterDataStatusPartial:
			partial = append(partial, c)
		case counterDataStatusMissing:
			missing = append(missing, c)
		}
	}
	if len(partial) == 0 && len(missing) == 0 {
		return ""
	}

	slices.SortFunc(partial, func(a, b counterSeries) int {
		return cmp.Compare(counterName(a.counter), counterName(b.counter))
	})
	slices.SortFunc(missing, func(a, b counterSeries) int {
		return cmp.Compare(counterName(a.counter), counterName(b.counter))
	})

	var out strings.Builder
	p := message.NewPrinter(language.English)
	if len(partial) > 0 {
		p.Fprintln(&out, "Partial (last):")
		for _, c := range partial {
			p.Fprintf(&out, "%v (%v)\n", counterName(c.counter), counterLastStatusTime(c).Format("Jan 2"))
		}
	}
	if len(missing) > 0 {
		if out.Len() > 0 {
			p.Fprintln(&out)
		}
		p.Fprintln(&out, "Missing (last):")
		for _, c := range missing {
			p.Fprintf(&out, "%v (%v)\n", counterName(c.counter), counterLastStatusTime(c).Format("Jan 2"))
		}
	}
	return strings.TrimSpace(out.String())
}

func counterLastStatusTime(c counterSeries) time.Time {
	last := c.last
	if !c.lastNonZero.IsZero() {
		last = c.lastNonZero
	}
	return last
}

type uvScriptHeatmaper struct{}

func (uvScriptHeatmaper) heatmap(ctx context.Context, day time.Time, cs []counterSeries) ([]byte, string, error) {
	hourOrder := make([]string, 0, 24)
	for i := 0; i < 24; i++ {
		hourOrder = append(hourOrder, fmt.Sprintf("%02d", i))
	}

	input := heatmapInput{
		Title:        fmt.Sprintf("Counts for %s by hour starting", day.Format("Mon Jan 2")),
		XLabel:       "Hour",
		YLabel:       "Counter",
		XValues:      hourOrder,
		CellWidth:    0.6,
		CellHeight:   0.6,
		Square:       true,
		Annotations:  true,
		ColorScale:   "sqrt",
		SortCounters: true,
	}

	for _, c := range cs {
		var values []heatmapInputValue
		for _, v := range c.series {
			values = append(values, heatmapInputValue{
				X:     fmt.Sprintf("%02d", v.tr.begin.Hour()),
				Count: v.val,
			})
		}
		name := cmp.Or(c.counter.ShortName, c.counter.Name)
		input.Counters = append(input.Counters, heatmapInputCounter{
			Name:    name,
			Missing: len(c.series) == 0,
			Values:  values,
		})
	}

	imgBytes, err := runUVScript(ctx, "heatmap.py", input)
	if err != nil {
		return nil, "", errutil.With(err)
	}
	return imgBytes, dailyAltText(cs), nil
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
	out := fmt.Sprintf("Heatmap of bikes counted by hour from the %s %s.", humanList(counterNames), counters)

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
