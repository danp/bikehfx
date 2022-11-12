package main

import (
	"context"
	"flag"
	"time"

	"github.com/peterbourgon/ff/v3/ffcli"
	"golang.org/x/text/message"
)

func newYearlyCmd(rootConfig *rootConfig) *ffcli.Command {
	var (
		fs      = flag.NewFlagSet("bikehfx-tweet yearly", flag.ExitOnError)
		year    = fs.String("year", time.Now().AddDate(-1, 0, 0).Format("2006"), "year to tweet for, in YYYY form")
		minYear = fs.String("min-year", "", "minimum year to consider when graphing, otherwise looks back 8 years")
		years   commaSeparatedString
	)
	fs.Var(&years, "years", "comma-separated years to tweet, in YYYY form, preferred over year")

	return &ffcli.Command{
		Name:       "yearly",
		ShortUsage: "bikehfx-tweet yearly",
		ShortHelp:  "send yearly tweet",
		FlagSet:    fs,
		Exec: func(ctx context.Context, args []string) error {
			years := years.vals
			if len(years) == 0 {
				years = []string{*year}
			}

			var mm time.Time
			if *minYear != "" {
				mmm, err := time.Parse("2006", *minYear)
				if err != nil {
					return err
				}
				mm = mmm
			}

			return yearlyExec(ctx, years, mm, rootConfig.ccd, rootConfig.trq, rootConfig.rc, rootConfig.twt)
		},
	}
}

func yearlyExec(ctx context.Context, years []string, minYear time.Time, ccd cyclingCounterDirectory, trq timeRangeQuerier, rc recordsChecker, twt tweetThread) error {
	loc, err := time.LoadLocation("America/Halifax")
	if err != nil {
		return err
	}

	if !minYear.IsZero() {
		minYear = time.Date(minYear.Year(), 1, 1, 0, 0, 0, 0, loc)
	}

	tweets := make([]tweet, 0, len(years))

	for _, year := range years {
		yeart, err := time.Parse("2006", year)
		if err != nil {
			return err
		}

		yearRange := newTimeRangeDate(time.Date(yeart.Year(), 1, 1, 0, 0, 0, 0, loc), 1, 0, 0)

		counters, err := ccd.counters(ctx, yearRange)
		if err != nil {
			return err
		}

		yearSeries, err := trq.queryCounterSeries(ctx, counters, []timeRange{yearRange})
		if err != nil {
			return err
		}

		records, err := rc.check(ctx, yearRange.begin, yearSeries, recordWidthYear)
		if err != nil {
			return err
		}

		mt := tweetText(yearSeries, records, func(p *message.Printer, sum string) string {
			return p.Sprintf("Year review:\n\n%s #bikehfx trips counted in %s", sum, yearRange.begin.Format("2006"))
		})

		graphBegin := yearRange.begin.AddDate(-7, 0, 0)
		if graphBegin.Before(minYear) {
			graphBegin = minYear
		}
		graphRange := newTimeRangeDate(graphBegin, 8, 0, 0)
		graphYears := graphRange.splitDate(1, 0, 0)

		graphCountRange := graphRange
		if graphCountRange.end.After(yearRange.end) {
			graphCountRange.end = yearRange.end
		}
		graphCountYears := graphCountRange.splitDate(1, 0, 0)

		graphCounters, err := ccd.counters(ctx, graphCountRange)
		if err != nil {
			return err
		}

		graphCountSeries, err := trq.queryCounterSeries(ctx, graphCounters, graphCountYears)
		if err != nil {
			return err
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

		var graphCountTRVs []timeRangeValue
		for _, gm := range graphCountYears {
			graphCountTRVs = append(graphCountTRVs, timeRangeValue{tr: gm, val: yearCounts[gm.begin]})
		}

		gr, err := timeRangeBarGraph(graphTRVs, "Total count by year", func(tr timeRange) string { return tr.begin.Format("2006") })
		if err != nil {
			return err
		}

		atg := altTextGenerator{
			headlinePrinter: func(p *message.Printer, len int) string {
				return p.Sprintf("Bar chart of counted cycling trips by year for last %d years.", len)
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
				return p.Sprintf("The most recent year had %d trips counted, %d%% %s than the previous year.", cur, pctChange, moreOrFewer)
			},
		}

		altText, err := atg.text(graphCountTRVs)
		if err != nil {
			return err
		}

		tweets = append(tweets, tweet{
			text: mt,
			media: []tweetMedia{
				{b: gr, altText: altText},
			},
		})
	}

	_, err = twt.tweetThread(ctx, tweets)
	return err
}
