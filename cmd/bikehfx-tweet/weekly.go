package main

import (
	"context"
	"flag"
	"time"

	"github.com/peterbourgon/ff/v3/ffcli"
	"golang.org/x/text/message"
)

func newWeeklyCmd(rootConfig *rootConfig) *ffcli.Command {
	var (
		fs    = flag.NewFlagSet("bikehfx-tweet weekly", flag.ExitOnError)
		week  = fs.String("week", time.Now().AddDate(0, 0, -7).Format("20060102"), "week to tweet for, in YYYYMMDD form")
		weeks commaSeparatedString
	)
	fs.Var(&weeks, "weeks", "comma-separated weeks to tweet, in YYYYMMDD form, preferred over week")

	return &ffcli.Command{
		Name:       "weekly",
		ShortUsage: "bikehfx-tweet weekly",
		ShortHelp:  "send weekly tweet",
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

func weeklyExec(ctx context.Context, weeks []string, ccd cyclingCounterDirectory, trq timeRangeQuerier, rc recordsChecker, twt tweetThread) error {
	loc, err := time.LoadLocation("America/Halifax")
	if err != nil {
		return err
	}

	var tweets []tweet
	for _, week := range weeks {
		weekt, err := time.Parse("20060102", week)
		if err != nil {
			return err
		}

		weekRange := newTimeRangeDate(time.Date(weekt.Year(), weekt.Month(), weekt.Day()-int(weekt.Weekday()), 0, 0, 0, 0, loc), 0, 0, 7)

		counters, err := ccd.counters(ctx, weekRange)
		if err != nil {
			return err
		}

		weekSeries, err := trq.queryCounterSeries(ctx, counters, []timeRange{weekRange})
		if err != nil {
			return err
		}

		records, err := rc.check(ctx, weekRange.begin, weekSeries, recordWidthWeek)
		if err != nil {
			return err
		}

		wt := tweetText(weekSeries, records, func(p *message.Printer, sum string) string {
			return p.Sprintf("Week review:\n\n%s #bikehfx trips counted week ending %s", sum, weekRange.end.AddDate(0, 0, -1).Format("Mon Jan 2"))
		})

		graphBegin := weekRange.begin.AddDate(0, 0, -7*7)
		graphRange := newTimeRangeDate(graphBegin, 0, 0, 8*7)
		graphWeeks := graphRange.splitDate(0, 0, 7)

		graphCounters, err := ccd.counters(ctx, graphRange)
		if err != nil {
			return err
		}

		graphCountSeries, err := trq.queryCounterSeries(ctx, graphCounters, graphWeeks)
		if err != nil {
			return err
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
			return err
		}

		atg := altTextGenerator{
			headlinePrinter: func(p *message.Printer, len int) string {
				return p.Sprintf("Bar chart of counted cycling trips by week for last %d weeks.", len)
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
				return p.Sprintf("The most recent week had %d trips counted, %d%% %s than the previous week.", cur, pctChange, moreOrFewer)
			},
		}

		altText, err := atg.text(graphTRVs)
		if err != nil {
			return err
		}

		tweets = append(tweets, tweet{
			text: wt,
			media: []tweetMedia{
				{b: gr, altText: altText},
			},
		})
	}

	_, err = twt.tweetThread(ctx, tweets)
	return err
}
