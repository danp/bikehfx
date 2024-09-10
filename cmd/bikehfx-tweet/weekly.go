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

func newWeeklyCmd(rootConfig *rootConfig) *ffcli.Command {
	var (
		fs    = flag.NewFlagSet("bikehfx-tweet weekly", flag.ExitOnError)
		week  = fs.String("week", time.Now().AddDate(0, 0, -7).Format("20060102"), "week to post for, in YYYYMMDD form")
		weeks commaSeparatedString
	)
	fs.Var(&weeks, "weeks", "comma-separated weeks to post, in YYYYMMDD form, preferred over week")

	return &ffcli.Command{
		Name:       "weekly",
		ShortUsage: "bikehfx-tweet weekly",
		ShortHelp:  "send weekly post",
		FlagSet:    fs,
		Exec: func(ctx context.Context, args []string) error {
			weeks := weeks.vals
			if len(weeks) == 0 {
				weeks = []string{*week}
			}

			return weeklyExec(ctx, weeks, rootConfig.ccd, rootConfig.trq, rootConfig.rc, rootConfig.twt)
		},
	}
}

func weeklyExec(ctx context.Context, weeks []string, ccd cyclingCounterDirectory, trq timeRangeQuerier, rc recordsChecker, pt postThread) error {
	loc, err := time.LoadLocation("America/Halifax")
	if err != nil {
		return errutil.With(err)
	}

	trq2 := counterbaseTimeRangeQuerierV2{ccd, trq}

	var posts []post
	for _, week := range weeks {
		weekt, err := time.ParseInLocation("20060102", week, loc)
		if err != nil {
			return errutil.With(err)
		}

		ps, err := weekPost(ctx, weekt, trq2, rc)
		if err != nil {
			return errutil.With(err)
		}

		posts = append(posts, ps...)
	}

	if _, err := pt.postThread(ctx, posts); err != nil {
		return errutil.With(err)
	}
	return nil
}

func weekPost(ctx context.Context, weekt time.Time, trq counterbaseTimeRangeQuerierV2, rc recordsChecker) ([]post, error) {
	var posts []post

	weekRange := newTimeRangeDate(time.Date(weekt.Year(), weekt.Month(), weekt.Day()-int(weekt.Weekday()), 0, 0, 0, 0, weekt.Location()), 0, 0, 7)

	weekRanges := []timeRange{weekRange}
	weekRangeYear, weekRangeNum := weekRange.begin.ISOWeek()
	for year := weekRangeYear - 1; year >= 2017 && len(weekRanges) < 8; year-- {
		diff := weekRangeYear - year
		pw := weekRange.addDate(-diff, 0, 0).startOfWeek()
		for {
			pwRangeYear, pwRangeNum := pw.begin.ISOWeek()
			if pwRangeYear == year && pwRangeNum == weekRangeNum {
				break
			}
			if pwRangeYear < year || pwRangeNum < weekRangeNum {
				pw = pw.addDate(0, 0, 7)
				continue
			}
			if pwRangeYear > year || pwRangeNum > weekRangeNum {
				pw = pw.addDate(0, 0, -7)
				continue
			}
		}
		weekRanges = append(weekRanges, pw)
	}

	var weeksSeries [][]counterSeriesV2
	for _, wr := range weekRanges {
		weekSeries, err := trq.query(ctx, wr)
		if err != nil {
			return nil, errutil.With(err)
		}

		weeksSeries = append(weeksSeries, weekSeries)
	}

	var cs1 []counterSeries
	for _, c := range weeksSeries[0] {
		if len(c.series) == 0 {
			continue
		}
		cs1 = append(cs1, counterSeries{
			counter: c.counter,
			series:  c.series,
		})
	}
	records, err := rc.check(ctx, weekRange.begin, cs1, recordWidthWeek)
	if err != nil {
		return nil, errutil.With(err)
	}

	weekPostText := weekPostText(weekRange, weeksSeries[0], records)

	graphBegin := weekRange.begin.AddDate(0, 0, -7*7)
	graphRange := newTimeRangeDate(graphBegin, 0, 0, 8*7)
	graphWeeks := graphRange.splitDate(0, 0, 7)

	graphCountSeries, err := trq.query(ctx, graphWeeks...)
	if err != nil {
		return nil, errutil.With(err)
	}

	weekCounts := make(map[time.Time]int)
	for _, cs := range graphCountSeries {
		for _, s := range cs.series {
			weekCounts[s.tr.begin] += s.val
		}
	}

	var graphTRVs []timeRangeValue
	for _, gm := range graphWeeks {
		graphTRVs = append(graphTRVs, timeRangeValue{tr: gm, val: weekCounts[gm.begin]})
	}

	gr, err := timeRangeBarGraph(graphTRVs, "Total count by week ending", func(tr timeRange) string { return tr.end.AddDate(0, 0, -1).Format("Jan 2") })
	if err != nil {
		return nil, errutil.With(err)
	}

	atg := altTextGenerator{
		headlinePrinter: func(p *message.Printer, len int) string {
			return p.Sprintf("Bar chart of bikes counted by week for last %d weeks.", len)
		},
		changePrinter: func(p *message.Printer, cur int, pctChange int) string {
			if pctChange == 0 {
				return p.Sprintf("The most recent week's count of %d is about the same as the previous week.", cur)
			}

			var moreOrFewer string
			if pctChange > 0 {
				moreOrFewer = "more"
			} else {
				moreOrFewer = "fewer"
				pctChange *= -1
			}
			return p.Sprintf("The most recent week had %d bikes counted, %d%% %s than the previous week.", cur, pctChange, moreOrFewer)
		},
	}

	altText, err := atg.text(graphTRVs)
	if err != nil {
		return nil, errutil.With(err)
	}

	posts = append(posts, post{
		text: weekPostText,
		media: []postMedia{
			{b: gr, altText: altText},
		},
	})

	var graph2TRVs []timeRangeValue
	for i, wr := range weekRanges {
		ws := weeksSeries[i]
		var sum int
		for _, cs := range ws {
			for _, s := range cs.series {
				sum += s.val
			}
		}
		graph2TRVs = append(graph2TRVs, timeRangeValue{tr: wr, val: sum})
	}

	prevWeeksPostPrinter := message.NewPrinter(language.English)
	prevWeeksPostText := prevWeeksPostPrinter.Sprintf("Previous year counts for week %d:\n\n", weekRangeNum)
	for _, trv := range graph2TRVs {
		prevWeeksPostText += prevWeeksPostPrinter.Sprintf("%v: %v\n", trv.tr.end.Format("2006"), trv.val)
	}

	slices.Reverse(graph2TRVs)
	gr2, err := timeRangeBarGraph(graph2TRVs, prevWeeksPostPrinter.Sprintf("Total count for week %d by year", weekRangeNum), func(tr timeRange) string { return tr.end.Format("2006") })
	if err != nil {
		return nil, errutil.With(err)
	}

	atg2 := altTextGenerator{
		headlinePrinter: func(p *message.Printer, len int) string {
			return p.Sprintf("Bar chart of bikes counted for week %d over last %d years.", weekRangeNum, len)
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

	const weeksPerYear = 52
	// using AddDate(-1, ...) would not maintain week boundaries
	pastThreeYears := timeRange{weekRange.begin.AddDate(0, 0, -(3*weeksPerYear)*7), weekRange.end}
	for {
		_, week := pastThreeYears.begin.ISOWeek()
		if week == 1 {
			break
		}
		pastThreeYears.begin = pastThreeYears.begin.AddDate(0, 0, -7)
	}

	pastThreeYearsWeeks := pastThreeYears.splitDate(0, 0, 7)
	pastThreeYearsWeeksSeries, err := trq.query(ctx, pastThreeYearsWeeks...)
	if err != nil {
		return nil, errutil.With(err)
	}

	var pastThreeYearsWeekCountsByYear = make(map[int]map[int]timeRangeValue)
	for _, cs := range pastThreeYearsWeeksSeries {
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
	}

	gr3, err := yearWeekChart(pastThreeYearsWeekCountsByYear, "Total count by week for recent years")
	if err != nil {
		return nil, errutil.With(err)
	}

	posts = append(posts, post{
		text: prevWeeksPostText,
		media: []postMedia{
			{b: gr2, altText: altText2},
			{b: gr3, altText: "Chart with line per year's total count by week for recent years"},
		},
	})

	return posts, nil
}

func weekPostText(weekRange timeRange, cs []counterSeriesV2, records map[string]recordKind) string {
	var out strings.Builder

	p := message.NewPrinter(language.English)

	var sum int
	presentIncompleteIndices := make(map[int]struct{})
	var presentIndices, missingIndices []int
	end := weekRange.end.AddDate(0, 0, -1)
	for i, c := range cs {
		for _, v := range c.series {
			sum += v.val
		}

		if c.last.Before(weekRange.begin) || c.lastNonZero.Before(weekRange.begin) {
			missingIndices = append(missingIndices, i)
			continue
		}

		presentIndices = append(presentIndices, i)
		if c.last.Before(end) || c.lastNonZero.Before(end) {
			presentIncompleteIndices[i] = struct{}{}
		}
	}

	p.Fprintf(&out, "Week review:\n\n%v%v #BikeHfx bikes counted week ending %v\n\n", sum, recordSymbol(records["sum"]), weekRange.end.AddDate(0, 0, -1).Format("Mon Jan 2"))

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
