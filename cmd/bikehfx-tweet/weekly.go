package main

import (
	"context"
	"flag"
	"time"

	"github.com/peterbourgon/ff/v3/ffcli"
	"golang.org/x/text/language"
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

		var weeksSeries [][]counterSeries
		for _, wr := range weekRanges {
			counters, err := ccd.counters(ctx, wr)
			if err != nil {
				return err
			}

			weekSeries, err := trq.queryCounterSeries(ctx, counters, []timeRange{wr})
			if err != nil {
				return err
			}

			weeksSeries = append(weeksSeries, weekSeries)
		}

		records, err := rc.check(ctx, weekRange.begin, weeksSeries[0], recordWidthWeek)
		if err != nil {
			return err
		}

		weekTweetText := tweetText(weeksSeries[0], records, func(p *message.Printer, sum string) string {
			return p.Sprintf("Week review:\n\n%s #BikeHfx trips counted week ending %s", sum, weekRange.end.AddDate(0, 0, -1).Format("Mon Jan 2"))
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
			text: weekTweetText,
			media: []tweetMedia{
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

		prevWeeksTweetPrinter := message.NewPrinter(language.English)
		prevWeeksTweetText := prevWeeksTweetPrinter.Sprintf("Previous year counts for week %d:\n\n", weekRangeNum)
		for _, trv := range graph2TRVs {
			prevWeeksTweetText += prevWeeksTweetPrinter.Sprintf("%v: %v\n", trv.tr.end.Format("2006"), trv.val)
		}

		reverse(graph2TRVs)
		gr2, err := timeRangeBarGraph(graph2TRVs, prevWeeksTweetPrinter.Sprintf("Total count for week %d by year", weekRangeNum), func(tr timeRange) string { return tr.end.Format("2006") })
		if err != nil {
			return err
		}

		atg2 := altTextGenerator{
			headlinePrinter: func(p *message.Printer, len int) string {
				return p.Sprintf("Bar chart of counted cycling trips for week %d over last %d years.", weekRangeNum, len)
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
			text: prevWeeksTweetText,
			media: []tweetMedia{
				{b: gr2, altText: altText2},
			},
		})
	}

	_, err = twt.tweetThread(ctx, tweets)
	return err
}
