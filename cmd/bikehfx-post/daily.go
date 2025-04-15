package main

import (
	"bytes"
	"cmp"
	"context"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/png"
	"log"
	"maps"
	"math"
	"os"
	"os/exec"
	"path/filepath"
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

		ps, err := dayPost(ctx, dayt, trq, ecWeatherer{}, rc, uvScriptGrapher{})
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

type dayGrapher interface {
	graph(ctx context.Context, day time.Time, cs []counterSeries) (_ []byte, _ image.Rectangle, altText string, _ error)
}

type counterSeries struct {
	counter     directory.Counter
	last        time.Time
	lastNonZero time.Time
	series      []timeRangeValue
}

func dayPost(ctx context.Context, day time.Time, trq counterbaseTimeRangeQuerier, weatherer weatherer, recordser recordser, grapher dayGrapher) ([]post, error) {
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

	dg, rect, dat, err := grapher.graph(ctx, day, hourSeries)
	if err != nil {
		return nil, errutil.With(err)
	}

	media := []postMedia{{
		b:       dg,
		width:   rect.Dx(),
		height:  rect.Dy(),
		altText: dat,
	}}

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

type uvScriptGrapher struct{}

//go:embed scripts/day-graph.py
var dayGraphScript []byte

//go:embed scripts/day-graph.py.lock
var dayGraphScriptLock []byte

func (uvScriptGrapher) graph(ctx context.Context, day time.Time, cs []counterSeries) ([]byte, image.Rectangle, string, error) {
	type inputCounterHour struct {
		Hour  int `json:"hour"`
		Count int `json:"count"`
	}
	type inputCounter struct {
		Name    string             `json:"name"`
		Missing bool               `json:"missing"`
		Hours   []inputCounterHour `json:"hours"`
	}
	var input struct {
		Day      string         `json:"day"`
		Counters []inputCounter `json:"counters"`
	}

	input.Day = day.Format("Mon Jan 2")
	for _, c := range cs {
		hours := []inputCounterHour{}
		for _, v := range c.series {
			hours = append(hours, inputCounterHour{Hour: v.tr.begin.Hour(), Count: v.val})
		}
		name := cmp.Or(c.counter.ShortName, c.counter.Name)
		input.Counters = append(input.Counters, inputCounter{Name: name, Hours: hours, Missing: len(c.series) == 0})
	}

	b, err := json.Marshal(input)
	if err != nil {
		return nil, image.Rectangle{}, "", errutil.With(err)
	}

	td, err := os.MkdirTemp("", "day-graph")
	if err != nil {
		return nil, image.Rectangle{}, "", errutil.With(err)
	}
	defer os.RemoveAll(td)

	if err := os.WriteFile(filepath.Join(td, "day-graph.py"), dayGraphScript, 0600); err != nil {
		return nil, image.Rectangle{}, "", errutil.With(err)
	}
	if err := os.Chmod(filepath.Join(td, "day-graph.py"), 0755); err != nil {
		return nil, image.Rectangle{}, "", errutil.With(err)
	}
	if err := os.WriteFile(filepath.Join(td, "day-graph.py.lock"), dayGraphScriptLock, 0600); err != nil {
		return nil, image.Rectangle{}, "", errutil.With(err)
	}

	var out bytes.Buffer

	cmd := exec.CommandContext(ctx, "uv", "run", "--quiet", "--frozen", filepath.Join(td, "day-graph.py")) //nolint:gosec
	cmd.Stdin = bytes.NewReader(b)
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return nil, image.Rectangle{}, "", errutil.With(err)
	}

	// get width and height from the output png
	img, err := png.Decode(bytes.NewReader(out.Bytes()))
	if err != nil {
		return nil, image.Rectangle{}, "", errutil.With(err)
	}

	return out.Bytes(), img.Bounds(), dailyAltText(cs), nil
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
