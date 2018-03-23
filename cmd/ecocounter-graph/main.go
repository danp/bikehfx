package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"time"

	"github.com/danp/bikehfx/ecocounter"
	chart "github.com/wcharczuk/go-chart"
	chartutil "github.com/wcharczuk/go-chart/util"
)

func main() {
	http.HandleFunc("/chart.png", graph)
	log.Fatal(http.ListenAndServe("127.0.0.1:8080", nil))
}

func graph(w http.ResponseWriter, r *http.Request) {
	counters := r.URL.Query()["counters"]
	if len(counters) == 0 {
		http.Error(w, "need counters", http.StatusBadRequest)
		return
	}

	bd, err := parseDate(r.URL.Query().Get("begin"))
	if err != nil {
		http.Error(w, "bad begin date", http.StatusBadRequest)
		return
	}

	ed, err := parseDate(r.URL.Query().Get("end"))
	if err != nil || ed.Before(bd) {
		http.Error(w, "bad end date", http.StatusBadRequest)
		return
	}

	var (
		cres   ecocounter.Resolution
		xform  chart.ValueFormatter
		title  string
		xtitle string
		ticks  []chart.Tick
	)

	switch r.URL.Query().Get("resolution") {
	case "day":
		if bd.Equal(ed) {
			http.Error(w, "must span at least two days for day resolution", http.StatusBadRequest)
			return
		}

		title = "Daily counts"
		xtitle = "Day"
		cres = ecocounter.ResolutionDay
		xform = timeFormatter("Mon 01-02")
		ticks = makeDayTicks(bd, ed) // only one tick per day
	case "hour":
		title = "Hourly counts"
		xtitle = "Hour"
		cres = ecocounter.ResolutionHour
		if bd.Equal(ed) {
			// if graph is for one day, only show hours
			xform = timeFormatter("3 PM")
		} else {
			xform = chart.TimeHourValueFormatter
		}
	default:
		http.Error(w, "bad resolution, try day or hour", http.StatusBadRequest)
		return
	}

	type dps struct {
		times  []time.Time
		values []float64
	}

	data := make(map[string]dps)
	var max int

	var cl ecocounter.Client

	for _, id := range counters {
		ds, err := cl.GetDatapoints(id, bd, ed, cres)
		if err != nil {
			log.Fatal(err)
		}

		var (
			times  []time.Time
			values []float64
			any    bool
		)

		for _, d := range ds {
			t, err := time.ParseInLocation("2006-01-02 15:04:05", d.Time, time.Local)
			if err != nil {
				log.Fatal(err)
			}

			times = append(times, t)
			values = append(values, float64(d.Count))
			if d.Count > max {
				max = d.Count
			}
			if d.Count > 0 {
				any = true
			}
		}

		if !any {
			continue // no data for this period, do not include
		}

		data[id] = dps{
			times:  times,
			values: values,
		}
	}

	counterIDsToNames := map[string]string{
		"100033965": "Agri SB",
		"100033028": "Uni Rowe",
		"100036476": "Uni Arts",
	}

	var timeSeries []chart.TimeSeries
	for id, dp := range data {
		name, ok := counterIDsToNames[id]
		if !ok {
			name = id
		}

		timeSeries = append(timeSeries, chart.TimeSeries{
			Name:    name,
			XValues: dp.times,
			YValues: dp.values,
		})
	}
	sort.Slice(timeSeries, func(i, j int) bool { return timeSeries[i].Name < timeSeries[j].Name })
	series := make([]chart.Series, 0, len(timeSeries))
	for i, s := range timeSeries {
		s.Style.StrokeDashArray = strokeDashArray(i)
		series = append(series, s)
	}

	graph := chart.Chart{
		Title:      title,
		TitleStyle: chart.StyleShow(),

		Background: chart.Style{
			// get the chart title outside the chart
			Padding: chart.Box{
				Top:   50,
				Right: 10,
				// get legend outside the chart
				Left:   100,
				Bottom: 10,
			},
		},

		XAxis: chart.XAxis{
			Name:           xtitle,
			NameStyle:      chart.StyleShow(),
			Style:          chart.StyleShow(),
			ValueFormatter: xform,
			Ticks:          ticks,
		},
		YAxis: chart.YAxis{
			Name:      "Count",
			NameStyle: chart.StyleShow(),
			Style:     chart.StyleShow(),
			ValueFormatter: func(v interface{}) string {
				// convert floats to whole numbers
				return fmt.Sprintf("%d", int(v.(float64)))
			},
			// specify the range so the min is 0
			Range: &chart.ContinuousRange{Max: float64(max)},
		},
		Series: series,
	}

	graph.Elements = []chart.Renderable{chart.LegendLeft(&graph)}

	w.Header().Set("Content-Type", "image/png")
	graph.Render(chart.PNG, w)
}

func parseDate(s string) (time.Time, error) {
	var t time.Time

	if s == "" {
		return t, errors.New("empty date")
	}

	t, err := time.ParseInLocation("20060102", s, time.Local)
	if err != nil {
		return t, fmt.Errorf("unable to parse date: %s", err)
	}

	return t, nil
}

func makeDayTicks(bd, ed time.Time) []chart.Tick {
	if ed.Sub(bd) > 4*24*time.Hour {
		// no need for a tick per day here
		return nil
	}

	var out []chart.Tick
	for !bd.After(ed) {
		out = append(out, chart.Tick{Value: chartutil.Time.ToFloat64(bd), Label: bd.Format("Mon 01-02")})
		bd = time.Date(bd.Year(), bd.Month(), bd.Day()+1, 0, 0, 0, 0, time.Local)
	}
	return out
}

func timeFormatter(f string) chart.ValueFormatter {
	return func(v interface{}) string {
		if typed, isTyped := v.(time.Time); isTyped {
			return typed.Format(f)
		}
		if typed, isTyped := v.(int64); isTyped {
			return time.Unix(0, typed).Format(f)
		}
		if typed, isTyped := v.(float64); isTyped {
			return time.Unix(0, int64(typed)).Format(f)
		}
		return ""
	}
}

func strokeDashArray(index int) []float64 {
	if index == 0 {
		return nil
	}
	return []float64{float64(index * 3), float64(index * 2)}
}
