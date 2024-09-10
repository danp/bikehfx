package main

import (
	"cmp"
	"context"
	"flag"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/graxinc/errutil"
	"github.com/peterbourgon/ff/v3/ffcli"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

func newYearlyCmd(rootConfig *rootConfig) *ffcli.Command {
	var (
		fs    = flag.NewFlagSet("bikehfx-tweet yearly", flag.ExitOnError)
		year  = fs.String("year", time.Now().AddDate(-1, 0, 0).Format("2006"), "year to post for, in YYYY form")
		years commaSeparatedString
	)
	fs.Var(&years, "years", "comma-separated years to post, in YYYY form, preferred over year")

	return &ffcli.Command{
		Name:       "yearly",
		ShortUsage: "bikehfx-tweet yearly",
		ShortHelp:  "send yearly post",
		FlagSet:    fs,
		Exec: func(ctx context.Context, args []string) error {
			years := years.vals
			if len(years) == 0 {
				years = []string{*year}
			}

			return yearlyExec(ctx, years, rootConfig.ccd, rootConfig.trq, rootConfig.rc, rootConfig.twt)
		},
	}
}

func yearlyExec(ctx context.Context, years []string, ccd cyclingCounterDirectory, trq timeRangeQuerier, rc recordsChecker, twt postThread) error {
	loc, err := time.LoadLocation("America/Halifax")
	if err != nil {
		return errutil.With(err)
	}

	trq2 := counterbaseTimeRangeQuerierV2{ccd, trq}

	var posts []post
	for _, year := range years {
		yeart, err := time.ParseInLocation("2006", year, loc)
		if err != nil {
			return errutil.With(err)
		}

		ts, err := yearPost(ctx, yeart, trq2, rc)
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

func yearPost(ctx context.Context, yeart time.Time, trq counterbaseTimeRangeQuerierV2, rc recordsChecker) ([]post, error) {
	var posts []post

	yearRange := newTimeRangeDate(time.Date(yeart.Year(), 1, 1, 0, 0, 0, 0, yeart.Location()), 1, 0, 0)

	yearRanges := []timeRange{yearRange}
	for year := yearRange.begin.Year() - 1; year >= 2017 && len(yearRanges) < 8; year-- {
		diff := yearRange.begin.Year() - year
		pw := yearRange.addDate(-diff, 0, 0)
		yearRanges = append(yearRanges, pw)
	}

	var yearsSeries [][]counterSeriesV2
	for _, wr := range yearRanges {
		yearSeries, err := trq.query(ctx, wr)
		if err != nil {
			return nil, errutil.With(err)
		}

		yearsSeries = append(yearsSeries, yearSeries)
	}

	var cs1 []counterSeries
	for _, c := range yearsSeries[0] {
		if len(c.series) == 0 {
			continue
		}
		cs1 = append(cs1, counterSeries{
			counter: c.counter,
			series:  c.series,
		})
	}
	records, err := rc.check(ctx, yearRange.begin, cs1, recordWidthYear)
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
		ws := yearsSeries[i]
		var sum int
		for _, cs := range ws {
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

	return posts, nil
}

func yearPostText(yearRange timeRange, cs []counterSeriesV2, records map[string]recordKind) string {
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
