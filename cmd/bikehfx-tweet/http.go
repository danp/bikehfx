package main

import (
	"context"
	"flag"
	"html/template"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/peterbourgon/ff/v3/ffcli"
	"golang.org/x/text/message"
)

func newHTTPCmd(rootConfig *rootConfig) *ffcli.Command {
	var (
		fs   = flag.NewFlagSet("bikehfx-tweet http", flag.ExitOnError)
		addr = fs.String("addr", "127.0.0.1:5000", "address to listen on")
	)
	return &ffcli.Command{
		Name:       "http",
		ShortUsage: "bikehfx-tweet http",
		ShortHelp:  "serve ui",
		FlagSet:    fs,
		Exec: func(ctx context.Context, args []string) error {
			return httpExec(ctx, *addr, rootConfig.trq, rootConfig.ccd, rootConfig.rc)
		},
	}
}

func httpExec(ctx context.Context, addr string, trq timeRangeQuerier, ccd cyclingCounterDirectory, rc recordsChecker) error {
	loc, err := time.LoadLocation("America/Halifax")
	if err != nil {
		return err
	}

	mux := http.NewServeMux()

	indexTmpl, err := template.New("index").Parse(`
<!DOCTYPE html>
<html>
  <head>
    <style type="text/css">
      body { font-family: arial }
      div { padding: 5px; margin 5px }
      h2 { margin-top: 0; margin-bottom: 10px }
    </style>
  <body>

    {{range $day := .}}
    <div>
      <h2>{{$day.Date}}</h2>
      <div>
        <img src="/graphs/daily/{{$day.Date}}"/>

        <pre>{{$day.Daily.Text}}</pre>

        {{if $day.Weekly.Text}}
        <img src="/graphs/weekly/{{$day.Date}}"/>

        <pre>{{$day.Weekly.Text}}</pre>
        {{end}}

        {{if $day.Monthly.Text}}
        <img src="/graphs/monthly/{{$day.Date}}"/>

        <pre>{{$day.Monthly.Text}}</pre>
        {{end}}
      </div>
    </div>
    {{end}}

  </body>
</html>
`)

	if err != nil {
		return err
	}

	type httpDay struct {
		Date string

		Daily struct {
			Text string
		}

		Weekly struct {
			Text string
		}

		Monthly struct {
			Text string
		}
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		sod := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)

		if before := r.URL.Query().Get("before"); before != "" {
			t, err := time.ParseInLocation("20060102", before, loc)
			if err != nil {
				http.Error(w, "can't parse before", 400)
				return
			}
			sod = t
		}

		rng := timeRange{begin: sod.AddDate(0, 0, -7), end: sod}
		rngDays := rng.splitDate(0, 0, 1)
		for i, j := 0, len(rngDays)-1; i < j; i, j = i+1, j-1 {
			rngDays[i], rngDays[j] = rngDays[j], rngDays[i]
		}

		var httpDays []httpDay
		for _, d := range rngDays {
			counters, err := ccd.counters(ctx, d)
			if err != nil {
				http.Error(w, "can't query counters", 500)
				return
			}

			daySeries, err := trq.queryCounterSeries(ctx, counters, []timeRange{d})
			if err != nil {
				http.Error(w, "can't query data", 500)
				return
			}

			records, err := rc.check(ctx, d.begin, daySeries, recordWidthDay)
			if err != nil {
				http.Error(w, "can't query records", 500)
				return
			}

			txt := tweetText(daySeries, records, func(p *message.Printer, sum string) string {
				return p.Sprintf("%s #bikehfx trips counted %s", sum, d.begin.Format("Mon Jan 2"))
			})

			hd := httpDay{Date: d.begin.Format("20060102")}
			hd.Daily.Text = txt

			if d.begin.Weekday() == time.Saturday {
				wr := newTimeRangeDate(d.begin.AddDate(0, 0, -6), 0, 0, 7)

				counters, err := ccd.counters(ctx, wr)
				if err != nil {
					http.Error(w, "can't query counters", 500)
					return
				}

				weekSeries, err := trq.queryCounterSeries(ctx, counters, []timeRange{wr})
				if err != nil {
					http.Error(w, "can't query data", 500)
					return
				}

				records, err := rc.check(ctx, wr.begin, weekSeries, recordWidthWeek)
				if err != nil {
					http.Error(w, "can't query records", 500)
					return
				}

				txt := tweetText(weekSeries, records, func(p *message.Printer, sum string) string {
					return p.Sprintf("Week review:\n\n%s #bikehfx trips counted week ending %s", sum, wr.end.AddDate(0, 0, -1).Format("Mon Jan 2"))
				})

				hd.Weekly.Text = txt
			}

			if d.begin.Day() == 1 {
				mr := newTimeRangeDate(d.begin, 0, 1, 0)

				counters, err := ccd.counters(ctx, mr)
				if err != nil {
					http.Error(w, "can't query counters", 500)
					return
				}

				monthSeries, err := trq.queryCounterSeries(ctx, counters, []timeRange{mr})
				if err != nil {
					http.Error(w, "can't query data", 500)
					return
				}

				records, err := rc.check(ctx, mr.begin, monthSeries, recordWidthMonth)
				if err != nil {
					http.Error(w, "can't query records", 500)
					return
				}

				txt := tweetText(monthSeries, records, func(p *message.Printer, sum string) string {
					return p.Sprintf("Month review:\n\n%s #bikehfx trips counted in %s", sum, mr.begin.Format("Jan"))
				})

				hd.Monthly.Text = txt
			}

			httpDays = append(httpDays, hd)
		}

		if err := indexTmpl.Execute(w, httpDays); err != nil {
			log.Println(err)
		}
	})

	mux.HandleFunc("/graphs/daily/", func(w http.ResponseWriter, r *http.Request) {
		dates := r.URL.Path[len("/graphs/daily/"):]

		date, err := time.ParseInLocation("20060102", dates, loc)
		if err != nil {
			http.Error(w, "bad date", 400)
			return
		}

		dayRange := newTimeRangeDate(date, 0, 0, 1)

		counters, err := ccd.counters(ctx, dayRange)
		if err != nil {
			http.Error(w, "can't load counters", 500)
			return
		}

		dayHours := dayRange.split(time.Hour)

		hourSeries, err := trq.queryCounterSeries(ctx, counters, dayHours)
		if err != nil {
			http.Error(w, "can't query data", 500)
			return
		}

		dg, err := dailyGraph(dayRange.begin, hourSeries)
		if err != nil {
			http.Error(w, "can't make graph", 500)
			return
		}
		defer dg.Close()

		w.Header().Set("Content-Type", "image/png")
		io.Copy(w, dg)
	})

	mux.HandleFunc("/graphs/weekly/", func(w http.ResponseWriter, r *http.Request) {
		dates := r.URL.Path[len("/graphs/weekly/"):]

		date, err := time.ParseInLocation("20060102", dates, loc)
		if err != nil {
			http.Error(w, "bad date", 400)
			return
		}
		date = date.AddDate(0, 0, -int(date.Weekday()))

		weekRange := newTimeRangeDate(date, 0, 0, 7)

		graphBegin := weekRange.begin.AddDate(0, 0, -7*7)
		graphRange := newTimeRangeDate(graphBegin, 0, 0, 8*7)
		graphWeeks := graphRange.splitDate(0, 0, 7)

		graphCounters, err := ccd.counters(ctx, graphRange)
		if err != nil {
			http.Error(w, "can't load counters", 500)
			return
		}

		graphCountSeries, err := trq.queryCounterSeries(ctx, graphCounters, graphWeeks)
		if err != nil {
			http.Error(w, "can't load data", 500)
			return
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
			http.Error(w, "can't make graph", 500)
			return
		}
		defer gr.Close()

		w.Header().Set("Content-Type", "image/png")
		io.Copy(w, gr)
	})

	mux.HandleFunc("/graphs/monthly/", func(w http.ResponseWriter, r *http.Request) {
		dates := r.URL.Path[len("/graphs/monthly/"):]

		date, err := time.ParseInLocation("20060102", dates, loc)
		if err != nil {
			http.Error(w, "bad date", 400)
			return
		}
		date = date.AddDate(0, 0, -date.Day()+1)

		monthRange := newTimeRangeDate(date, 0, 1, 0)

		graphBegin := monthRange.begin.AddDate(0, -7, 0)
		graphRange := newTimeRangeDate(graphBegin, 0, 8, 0)
		graphMonths := graphRange.splitDate(0, 1, 0)

		graphCounters, err := ccd.counters(ctx, graphRange)
		if err != nil {
			http.Error(w, "can't load counters", 500)
			return
		}

		graphCountSeries, err := trq.queryCounterSeries(ctx, graphCounters, graphMonths)
		if err != nil {
			http.Error(w, "can't load data", 500)
			return
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
			http.Error(w, "can't make graph", 500)
			return
		}
		defer gr.Close()

		w.Header().Set("Content-Type", "image/png")
		io.Copy(w, gr)
	})

	return http.ListenAndServe(addr, mux)
}
