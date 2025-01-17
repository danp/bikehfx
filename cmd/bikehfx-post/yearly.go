package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/danp/counterbase/directory"
	"github.com/graxinc/errutil"
	"github.com/peterbourgon/ff/v3/ffcli"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

func newYearlyCmd(rootConfig *rootConfig) *ffcli.Command {
	var (
		fs    = flag.NewFlagSet("bikehfx-post yearly", flag.ExitOnError)
		year  = fs.String("year", time.Now().AddDate(-1, 0, 0).Format("2006"), "year to post for, in YYYY form")
		years commaSeparatedString
	)
	fs.Var(&years, "years", "comma-separated years to post, in YYYY form, preferred over year")

	return &ffcli.Command{
		Name:       "yearly",
		ShortUsage: "bikehfx-post yearly",
		ShortHelp:  "send yearly post",
		FlagSet:    fs,
		Exec: func(ctx context.Context, args []string) error {
			years := years.vals
			if len(years) == 0 {
				years = []string{*year}
			}

			return yearlyExec(ctx, years, rootConfig.trq, rootConfig.rc, rootConfig.tp)
		},
	}
}

func yearlyExec(ctx context.Context, years []string, trq counterbaseTimeRangeQuerier, rc recordser, tp threadPoster) error {
	loc, err := time.LoadLocation("America/Halifax")
	if err != nil {
		return errutil.With(err)
	}

	var posts []post
	for _, year := range years {
		yeart, err := time.ParseInLocation("2006", year, loc)
		if err != nil {
			return errutil.With(err)
		}

		ps, err := yearPost(ctx, yeart, trq, rc)
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

func yearPost(ctx context.Context, yeart time.Time, trq counterbaseTimeRangeQuerier, rc recordser) ([]post, error) {
	var posts []post

	yearRange := newTimeRangeDate(time.Date(yeart.Year(), 1, 1, 0, 0, 0, 0, yeart.Location()), 1, 0, 0)

	yearRanges := []timeRange{yearRange}
	for year := yearRange.begin.Year() - 1; year >= 2017 && len(yearRanges) < 8; year-- {
		diff := yearRange.begin.Year() - year
		pw := yearRange.addDate(-diff, 0, 0)
		yearRanges = append(yearRanges, pw)
	}

	var yearsSeries [][]counterSeries
	for _, wr := range yearRanges {
		yearSeries, err := trq.query(ctx, wr)
		if err != nil {
			return nil, errutil.With(err)
		}

		yearsSeries = append(yearsSeries, yearSeries)
	}

	var cs []counterSeries
	for _, c := range yearsSeries[0] {
		if len(c.series) == 0 {
			continue
		}
		cs = append(cs, counterSeries{
			counter: c.counter,
			series:  c.series,
		})
	}
	records, err := rc.records(ctx, yearRange.begin, cs, recordWidthYear)
	if err != nil {
		return nil, errutil.With(err)
	}

	yearPostText := yearPostText(yearRange, yearsSeries[0], records)

	graphBegin := yearRange.begin.AddDate(-7, 0, 0)
	graphRange := newTimeRangeDate(graphBegin, 8, 0, 0)
	graphYears := graphRange.splitDate(1, 0, 0)

	graphCountSeries, err := trq.query(ctx, graphYears...)
	if err != nil {
		return nil, errutil.With(err)
	}

	yearCounts := make(map[time.Time]int)
	for _, cs := range graphCountSeries {
		for _, s := range cs.series {
			yearCounts[s.tr.begin] += s.val
		}
	}

	var graphTRVs []timeRangeValue
	for _, gm := range graphYears {
		graphTRVs = append(graphTRVs, timeRangeValue{tr: gm, val: yearCounts[gm.begin]})
	}

	gr, err := timeRangeBarGraph(graphTRVs, "Total count by year", func(tr timeRange) string { return tr.begin.Format("2006") })
	if err != nil {
		return nil, errutil.With(err)
	}

	atg := altTextGenerator{
		headlinePrinter: func(p *message.Printer, len int) string {
			return p.Sprintf("Bar chart of bikes counted by year for last %d years.", len)
		},
		changePrinter: func(p *message.Printer, cur int, pctChange int) string {
			if pctChange == 0 {
				return p.Sprintf("The most recent year's count of %d is about the same as the previous year.", cur)
			}

			var moreOrFewer string
			if pctChange > 0 {
				moreOrFewer = "more"
			} else {
				moreOrFewer = "fewer"
				pctChange *= -1
			}
			return p.Sprintf("The most recent year had %d bikes counted, %d%% %s than the previous year.", cur, pctChange, moreOrFewer)
		},
	}

	altText, err := atg.text(graphTRVs)
	if err != nil {
		return nil, errutil.With(err)
	}

	posts = append(posts, post{
		text: yearPostText,
		media: []postMedia{
			{b: gr, altText: altText},
		},
	})

	var graph2TRVs []timeRangeValue
	for i, wr := range yearRanges {
		ys := yearsSeries[i]
		var sum int
		for _, cs := range ys {
			for _, s := range cs.series {
				sum += s.val
			}
		}
		graph2TRVs = append(graph2TRVs, timeRangeValue{tr: wr, val: sum})
	}

	prevYearsPostPrinter := message.NewPrinter(language.English)
	prevYearsPostText := prevYearsPostPrinter.Sprintf("Previous year counts:\n\n")
	for _, trv := range graph2TRVs {
		prevYearsPostText += prevYearsPostPrinter.Sprintf("%v: %v\n", trv.tr.begin.Format("2006"), trv.val)
	}

	slices.Reverse(graph2TRVs)
	gr2, err := timeRangeBarGraph(graph2TRVs, prevYearsPostPrinter.Sprintf("Total count by year"), func(tr timeRange) string { return tr.begin.Format("2006") })
	if err != nil {
		return nil, errutil.With(err)
	}

	atg2 := altTextGenerator{
		headlinePrinter: func(p *message.Printer, len int) string {
			return p.Sprintf("Bar chart of bikes counted over last %d years.", len)
		},
		changePrinter: func(p *message.Printer, cur int, pctChange int) string {
			if pctChange == 0 {
				return p.Sprintf("The most recent years's count of %d is about the same as the previous year.", cur)
			}

			var moreOrFewer string
			if pctChange > 0 {
				moreOrFewer = "more"
			} else {
				moreOrFewer = "fewer"
				pctChange *= -1
			}
			return p.Sprintf("The most recent year had %d bikes counted, %d%% %s than the previous year.", cur, pctChange, moreOrFewer)
		},
	}

	altText2, err := atg2.text(graph2TRVs)
	if err != nil {
		return nil, errutil.With(err)
	}

	posts = append(posts, post{
		text: prevYearsPostText,
		media: []postMedia{
			{b: gr2, altText: altText2},
		},
	})

	const weeksPerYear = 52
	// using AddDate(-1, ...) would not maintain week boundaries
	pastThreeYears := timeRange{yearRange.begin.AddDate(0, 0, -(3*weeksPerYear)*7), yearRange.end}
	for pastThreeYears.end.Weekday() != time.Sunday { // find last complete week
		pastThreeYears.end = pastThreeYears.end.AddDate(0, 0, -1)
	}
	for {
		_, week := pastThreeYears.begin.ISOWeek()
		if week == 1 {
			break
		}
		pastThreeYears.begin = pastThreeYears.begin.AddDate(0, 0, -7)
	}
	if pastThreeYears.begin.Weekday() >= time.Thursday {
		// get to sunday
		pastThreeYears.begin = pastThreeYears.begin.AddDate(0, 0, 7-int(pastThreeYears.begin.Weekday()))
	} else {
		// get to sunday
		pastThreeYears.begin = pastThreeYears.begin.AddDate(0, 0, -int(pastThreeYears.begin.Weekday()))
	}

	pastThreeYearsWeeks := pastThreeYears.splitDate(0, 0, 7)
	pastThreeYearsWeeksSeries, err := trq.query(ctx, pastThreeYearsWeeks...)
	if err != nil {
		return nil, errutil.With(err)
	}

	countersByID := make(map[string]directory.Counter)
	pastThreeYearsWeekCountsByCounterByYear := make(map[string]map[int]map[int]timeRangeValue)
	for _, cs := range pastThreeYearsWeeksSeries {
		countersByID[cs.counter.ID] = cs.counter
		pastThreeYearsWeekCountsByYear := make(map[int]map[int]timeRangeValue)
		for _, s := range cs.series {
			year, week := s.tr.end.ISOWeek()
			if pastThreeYearsWeekCountsByYear[year] == nil {
				pastThreeYearsWeekCountsByYear[year] = make(map[int]timeRangeValue)
			}
			v, ok := pastThreeYearsWeekCountsByYear[year][week]
			if !ok {
				pastThreeYearsWeekCountsByYear[year][week] = s
				continue
			}
			v.val += s.val
			pastThreeYearsWeekCountsByYear[year][week] = v
		}
		pastThreeYearsWeekCountsByCounterByYear[cs.counter.ID] = pastThreeYearsWeekCountsByYear
	}

	counterIDs := slices.Collect(maps.Keys(countersByID))
	slices.SortFunc(counterIDs, func(a, b string) int {
		return cmp.Compare(counterName(countersByID[a]), counterName(countersByID[b]))
	})

	for _, id := range counterIDs {
		c := countersByID[id]

		var graph2TRVs []timeRangeValue
		for i, wr := range yearRanges {
			ys := yearsSeries[i]
			var sum int
			for _, cs := range ys {
				if cs.counter.ID != id {
					continue
				}
				for _, s := range cs.series {
					sum += s.val
				}
			}
			graph2TRVs = append(graph2TRVs, timeRangeValue{tr: wr, val: sum})
		}

		prevYearsPostPrinter := message.NewPrinter(language.English)
		prevYearsPostText := prevYearsPostPrinter.Sprintf("Previous year counts for %v:\n\n", counterName(c))
		for _, trv := range graph2TRVs {
			if trv.val == 0 {
				continue
			}
			prevYearsPostText += prevYearsPostPrinter.Sprintf("%v: %v\n", trv.tr.begin.Format("2006"), trv.val)
		}

		slices.Reverse(graph2TRVs)
		gr2, err := timeRangeBarGraph(graph2TRVs, prevYearsPostPrinter.Sprintf("Total count by year for %v", counterName(c)), func(tr timeRange) string { return tr.begin.Format("2006") })
		if err != nil {
			return nil, errutil.With(err)
		}

		atg2 := altTextGenerator{
			headlinePrinter: func(p *message.Printer, len int) string {
				return p.Sprintf("Bar chart of bikes counted for %v over last %d years.", counterName(c), len)
			},
			changePrinter: func(p *message.Printer, cur int, pctChange int) string {
				if pctChange == 0 {
					return p.Sprintf("The most recent years's count of %d is about the same as the previous year.", cur)
				}

				var moreOrFewer string
				if pctChange > 0 {
					moreOrFewer = "more"
				} else {
					moreOrFewer = "fewer"
					pctChange *= -1
				}
				return p.Sprintf("The most recent year had %d bikes counted, %d%% %s than the previous year.", cur, pctChange, moreOrFewer)
			},
		}

		altText2, err := atg2.text(graph2TRVs)
		if err != nil {
			return nil, errutil.With(err)
		}

		pastThreeYearsWeekCountsByYear := pastThreeYearsWeekCountsByCounterByYear[id]
		years := slices.Collect(maps.Keys(pastThreeYearsWeekCountsByYear))
		slices.Sort(years)
	InitialZeroesClean:
		for _, year := range years {
			weekCounts := pastThreeYearsWeekCountsByYear[year]
			weeks := slices.Collect(maps.Keys(weekCounts))
			slices.Sort(weeks)
			for _, week := range weeks {
				if weekCounts[week].val > 0 {
					break InitialZeroesClean
				}
				delete(weekCounts, week)
				if len(weekCounts) == 0 {
					delete(pastThreeYearsWeekCountsByYear, year)
					break
				}
			}
		}
		gr3, err := yearWeekChart(pastThreeYearsWeekCountsByYear, fmt.Sprintf("Total count by week for %v for recent years", counterName(c)))
		if err != nil {
			return nil, errutil.With(err)
		}

		posts = append(posts, post{
			text: prevYearsPostText,
			media: []postMedia{
				{b: gr2, altText: altText2},
				{b: gr3, altText: fmt.Sprintf("Chart with line per year's total count by week for %v for recent years.", counterName(c))},
			},
		})
	}

	return posts, nil
}

func yearPostText(yearRange timeRange, cs []counterSeries, records map[string]recordKind) string {
	var out strings.Builder

	p := message.NewPrinter(language.English)

	var sum int
	presentIncompleteIndices := make(map[int]struct{})
	var presentIndices, missingIndices []int
	end := yearRange.end.AddDate(0, 0, -1)
	for i, c := range cs {
		for _, v := range c.series {
			sum += v.val
		}

		if c.last.Before(yearRange.begin) || c.lastNonZero.Before(yearRange.begin) {
			missingIndices = append(missingIndices, i)
			continue
		}

		presentIndices = append(presentIndices, i)
		if c.last.Before(end) || c.lastNonZero.Before(end) {
			presentIncompleteIndices[i] = struct{}{}
		}
	}

	p.Fprintf(&out, "Year review:\n\n%v%v #BikeHfx bikes counted in %v\n\n", sum, recordSymbol(records["sum"]), yearRange.begin.Format("2006"))

	slices.SortFunc(presentIndices, func(i, j int) int {
		return cmp.Compare(counterName(cs[i].counter), counterName(cs[j].counter))
	})
	for _, i := range presentIndices {
		c := cs[i]
		v := c.series[len(c.series)-1].val
		p.Fprintf(&out, "%v%v %v", v, recordSymbol(records[c.counter.ID]), counterName(c.counter))
		if _, ok := presentIncompleteIndices[i]; ok {
			last := c.last
			if !c.lastNonZero.IsZero() {
				last = c.lastNonZero
			}
			p.Fprintf(&out, " (last %v)", last.Format("Jan 2"))
		}
		p.Fprintln(&out)
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
