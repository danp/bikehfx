package main

import (
	"cmp"
	"context"
	"flag"
	"slices"
	"strings"
	"time"

	"github.com/graxinc/errutil"
	"github.com/peterbourgon/ff/v3/ffcli"
	"golang.org/x/exp/maps"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

func newMonthlyCmd(rootConfig *rootConfig) *ffcli.Command {
	var (
		fs     = flag.NewFlagSet("bikehfx-tweet monthly", flag.ExitOnError)
		month  = fs.String("month", time.Now().AddDate(0, -1, 0).Format("200601"), "month to post for, in YYYYMM form")
		months commaSeparatedString
	)
	fs.Var(&months, "months", "comma-separated months to post, in YYYYMM form, preferred over month")

	return &ffcli.Command{
		Name:       "monthly",
		ShortUsage: "bikehfx-tweet monthly",
		ShortHelp:  "send monthly post",
		FlagSet:    fs,
		Exec: func(ctx context.Context, args []string) error {
			months := months.vals
			if len(months) == 0 {
				months = []string{*month}
			}

			return monthlyExec(ctx, months, rootConfig.ccd, rootConfig.trq, rootConfig.rc, rootConfig.twt)
		},
	}
}

func monthlyExec(ctx context.Context, months []string, ccd cyclingCounterDirectory, trq timeRangeQuerier, rc recordsChecker, twt postThread) error {
	loc, err := time.LoadLocation("America/Halifax")
	if err != nil {
		return errutil.With(err)
	}

	trq2 := counterbaseTimeRangeQuerierV2{ccd, trq}

	var posts []post
	for _, month := range months {
		montht, err := time.ParseInLocation("200601", month, loc)
		if err != nil {
			return errutil.With(err)
		}

		ts, err := monthPost(ctx, montht, trq2, rc)
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

func monthPost(ctx context.Context, montht time.Time, trq counterbaseTimeRangeQuerierV2, rc recordsChecker) ([]post, error) {
	var posts []post

	monthRange := newTimeRangeDate(time.Date(montht.Year(), montht.Month(), 1, 0, 0, 0, 0, montht.Location()), 0, 1, 0)

	monthRanges := []timeRange{monthRange}
	for year := monthRange.begin.Year() - 1; year >= 2017 && len(monthRanges) < 8; year-- {
		diff := monthRange.begin.Year() - year
		pw := monthRange.addDate(-diff, 0, 0)
		monthRanges = append(monthRanges, pw)
	}

	var monthsSeries [][]counterSeriesV2
	for _, wr := range monthRanges {
		monthSeries, err := trq.query(ctx, wr)
		if err != nil {
			return nil, errutil.With(err)
		}

		monthsSeries = append(monthsSeries, monthSeries)
	}

	var cs1 []counterSeries
	for _, c := range monthsSeries[0] {
		if len(c.series) == 0 {
			continue
		}
		cs1 = append(cs1, counterSeries{
			counter: c.counter,
			series:  c.series,
		})
	}
	records, err := rc.check(ctx, monthRange.begin, cs1, recordWidthMonth)
	if err != nil {
		return nil, errutil.With(err)
	}

	monthPostText := monthPostText(monthRange, monthsSeries[0], records)

	graphBegin := monthRange.begin.AddDate(0, -7, 0)
	graphRange := newTimeRangeDate(graphBegin, 0, 8, 0)
	graphMonths := graphRange.splitDate(0, 1, 0)

	graphCountSeries, err := trq.query(ctx, graphMonths...)
	if err != nil {
		return nil, errutil.With(err)
	}

	monthCounts := make(map[time.Time]int)
	for _, cs := range graphCountSeries {
		for _, s := range cs.series {
			monthCounts[s.tr.begin] += s.val
		}
	}

	var graphTRVs []timeRangeValue
	for _, gm := range graphMonths {
		graphTRVs = append(graphTRVs, timeRangeValue{tr: gm, val: monthCounts[gm.begin]})
	}

	gr, err := timeRangeBarGraph(graphTRVs, "Total count by month", func(tr timeRange) string { return tr.begin.Format("Jan") })
	if err != nil {
		return nil, errutil.With(err)
	}

	atg := altTextGenerator{
		headlinePrinter: func(p *message.Printer, len int) string {
			return p.Sprintf("Bar chart of counted cycling trips by month for last %d months.", len)
		},
		changePrinter: func(p *message.Printer, cur int, pctChange int) string {
			if pctChange == 0 {
				return p.Sprintf("The most recent month's count of %d is about the same as the previous month.", cur)
			}

			var moreOrFewer string
			if pctChange > 0 {
				moreOrFewer = "more"
			} else {
				moreOrFewer = "fewer"
				pctChange *= -1
			}
			return p.Sprintf("The most recent month had %d trips counted, %d%% %s than the previous month.", cur, pctChange, moreOrFewer)
		},
	}

	altText, err := atg.text(graphTRVs)
	if err != nil {
		return nil, errutil.With(err)
	}

	posts = append(posts, post{
		text: monthPostText,
		media: []postMedia{
			{b: gr, altText: altText},
		},
	})

	var graph2TRVs []timeRangeValue
	for i, wr := range monthRanges {
		ws := monthsSeries[i]
		var sum int
		for _, cs := range ws {
			for _, s := range cs.series {
				sum += s.val
			}
		}
		graph2TRVs = append(graph2TRVs, timeRangeValue{tr: wr, val: sum})
	}

	prevMonthsPostPrinter := message.NewPrinter(language.English)
	prevMonthsPostText := prevMonthsPostPrinter.Sprintf("Previous year counts for %v:\n\n", monthRange.begin.Format("Jan"))
	for _, trv := range graph2TRVs {
		prevMonthsPostText += prevMonthsPostPrinter.Sprintf("%v: %v\n", trv.tr.begin.Format("2006"), trv.val)
	}

	slices.Reverse(graph2TRVs)
	gr2, err := timeRangeBarGraph(graph2TRVs, prevMonthsPostPrinter.Sprintf("Total count for month %v by year", monthRange.begin.Format("Jan")), func(tr timeRange) string { return tr.begin.Format("2006") })
	if err != nil {
		return nil, errutil.With(err)
	}

	atg2 := altTextGenerator{
		headlinePrinter: func(p *message.Printer, len int) string {
			return p.Sprintf("Bar chart of counted cycling trips for month %v over last %d years.", monthRange.begin.Format("Jan"), len)
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
			return p.Sprintf("The most recent year had %d trips counted, %d%% %s than the previous year.", cur, pctChange, moreOrFewer)
		},
	}

	altText2, err := atg2.text(graph2TRVs)
	if err != nil {
		return nil, errutil.With(err)
	}

	posts = append(posts, post{
		text: prevMonthsPostText,
		media: []postMedia{
			{b: gr2, altText: altText2},
		},
	})

	return posts, nil
}

func monthPostText(monthRange timeRange, cs []counterSeriesV2, records map[string]recordKind) string {
	var out strings.Builder

	p := message.NewPrinter(language.English)

	var sum int
	presentIncompleteIndices := make(map[int]struct{})
	var presentIndices, missingIndices []int
	end := monthRange.end.AddDate(0, 0, -1)
	for i, c := range cs {
		for _, v := range c.series {
			sum += v.val
		}

		if c.last.Before(monthRange.begin) || c.lastNonZero.Before(monthRange.begin) {
			missingIndices = append(missingIndices, i)
			continue
		}

		presentIndices = append(presentIndices, i)
		if c.last.Before(end) || c.lastNonZero.Before(end) {
			presentIncompleteIndices[i] = struct{}{}
		}
	}

	p.Fprintf(&out, "Month review:\n\n%v%v #BikeHfx trips counted in %v\n\n", sum, recordSymbol(records["sum"]), monthRange.begin.Format("Jan"))

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
		keys := maps.Keys(recordKinds)
		slices.Sort(keys)
		p.Fprintln(&out)
		for _, k := range keys {
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
