package main

import (
	"context"
	"flag"
	"slices"
	"time"

	"github.com/peterbourgon/ff/v3/ffcli"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

func newMonthlyCmd(rootConfig *rootConfig) *ffcli.Command {
	var (
		fs       = flag.NewFlagSet("bikehfx-tweet monthly", flag.ExitOnError)
		month    = fs.String("month", time.Now().AddDate(0, -1, 0).Format("200601"), "month to tweet for, in YYYYMM form")
		minMonth = fs.String("min-month", "", "minimum month to consider when graphing, otherwise looks back 8 months")
		months   commaSeparatedString
	)
	fs.Var(&months, "months", "comma-separated months to tweet, in YYYYMM form, preferred over month")

	return &ffcli.Command{
		Name:       "monthly",
		ShortUsage: "bikehfx-tweet monthly",
		ShortHelp:  "send monthly tweet",
		FlagSet:    fs,
		Exec: func(ctx context.Context, args []string) error {
			months := months.vals
			if len(months) == 0 {
				months = []string{*month}
			}

			var mm time.Time
			if *minMonth != "" {
				mmm, err := time.Parse("200601", *minMonth)
				if err != nil {
					return err
				}
				mm = mmm
			}

			return monthlyExec(ctx, months, mm, rootConfig.ccd, rootConfig.trq, rootConfig.rc, rootConfig.twt)
		},
	}
}

func monthlyExec(ctx context.Context, months []string, minMonth time.Time, ccd cyclingCounterDirectory, trq timeRangeQuerier, rc recordsChecker, twt tweetThread) error {
	loc, err := time.LoadLocation("America/Halifax")
	if err != nil {
		return err
	}

	if !minMonth.IsZero() {
		minMonth = time.Date(minMonth.Year(), minMonth.Month(), 1, 0, 0, 0, 0, loc)
	}

	tweets := make([]tweet, 0, len(months))

	for _, month := range months {
		montht, err := time.Parse("200601", month)
		if err != nil {
			return err
		}

		monthRange := newTimeRangeDate(time.Date(montht.Year(), montht.Month(), 1, 0, 0, 0, 0, loc), 0, 1, 0)

		monthRanges := []timeRange{monthRange}
		for year := monthRange.begin.Year() - 1; year >= 2017 && len(monthRanges) < 8; year-- {
			diff := monthRange.begin.Year() - year
			monthRanges = append(monthRanges, monthRange.addDate(-diff, 0, 0))
		}

		var monthsSeries [][]counterSeries
		for _, mr := range monthRanges {
			counters, err := ccd.counters(ctx, mr)
			if err != nil {
				return err
			}

			monthSeries, err := trq.queryCounterSeries(ctx, counters, []timeRange{mr})
			if err != nil {
				return err
			}

			monthsSeries = append(monthsSeries, monthSeries)
		}

		records, err := rc.check(ctx, monthRange.begin, monthsSeries[0], recordWidthMonth)
		if err != nil {
			return err
		}

		monthTweetText := tweetText(monthsSeries[0], records, func(p *message.Printer, sum string) string {
			return p.Sprintf("Month review:\n\n%s #bikehfx trips counted in %s", sum, monthRange.begin.Format("Jan"))
		})

		graphBegin := monthRange.begin.AddDate(0, -7, 0)
		if graphBegin.Before(minMonth) {
			graphBegin = minMonth
		}
		graphRange := newTimeRangeDate(graphBegin, 0, 8, 0)
		graphMonths := graphRange.splitDate(0, 1, 0)

		graphCountRange := graphRange
		if graphCountRange.end.After(monthRange.end) {
			graphCountRange.end = monthRange.end
		}
		graphCountMonths := graphCountRange.splitDate(0, 1, 0)

		graphCounters, err := ccd.counters(ctx, graphCountRange)
		if err != nil {
			return err
		}

		graphCountSeries, err := trq.queryCounterSeries(ctx, graphCounters, graphCountMonths)
		if err != nil {
			return err
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

		var graphCountTRVs []timeRangeValue
		for _, gm := range graphCountMonths {
			graphCountTRVs = append(graphCountTRVs, timeRangeValue{tr: gm, val: monthCounts[gm.begin]})
		}

		gr, err := timeRangeBarGraph(graphTRVs, "Total count by month", func(tr timeRange) string { return tr.begin.Format("Jan") })
		if err != nil {
			return err
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

		altText, err := atg.text(graphCountTRVs)
		if err != nil {
			return err
		}

		tweets = append(tweets, tweet{
			text: monthTweetText,
			media: []tweetMedia{
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

		prevMonthsTweetPrinter := message.NewPrinter(language.English)
		prevMonthsTweetText := prevMonthsTweetPrinter.Sprintf("Previous year counts for %s:\n\n", monthRange.begin.Format("Jan"))
		for _, trv := range graph2TRVs {
			prevMonthsTweetText += prevMonthsTweetPrinter.Sprintf("%s: %d\n", trv.tr.end.Format("2006"), trv.val)
		}

		slices.Reverse(graph2TRVs)
		gr2, err := timeRangeBarGraph(graph2TRVs, prevMonthsTweetPrinter.Sprintf("Total count for %s by year", monthRange.begin.Format("Jan")), func(tr timeRange) string { return tr.end.Format("2006") })
		if err != nil {
			return err
		}

		atg2 := altTextGenerator{
			headlinePrinter: func(p *message.Printer, len int) string {
				return p.Sprintf("Bar chart of counted cycling trips for %s over last %d years.", monthRange.begin.Format("Jan"), len)
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
			return err
		}

		tweets = append(tweets, tweet{
			text: prevMonthsTweetText,
			media: []tweetMedia{
				{b: gr2, altText: altText2},
			},
		})
	}

	_, err = twt.tweetThread(ctx, tweets)
	return err
}
