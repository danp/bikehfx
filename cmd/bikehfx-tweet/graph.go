package main

import (
	"bytes"
	"fmt"
	"time"

	"github.com/danp/bikehfx/ecocounter"
	chart "github.com/wcharczuk/go-chart"
)

func makeHourlyGraph(cl *ecocounter.Client, day time.Time) ([]byte, error) {
	var (
		series = []chart.Series{}
		max    int
	)

	for i, c := range publicCounters {
		ds, err := cl.GetDatapoints(c.ecoID, day, day, ecocounter.ResolutionHour)
		if err != nil {
			return nil, err
		}

		ts := chart.TimeSeries{
			Name: c.name(),
			Style: chart.Style{
				StrokeDashArray: strokeDashArray(i),
			},
		}

		var any bool
		for _, d := range ds {
			t, err := time.ParseInLocation("2006-01-02 15:04:05", d.Time, time.Local)
			if err != nil {
				return nil, err
			}

			if d.Count > 0 {
				any = true
			}
			if d.Count > max {
				max = d.Count
			}

			ts.XValues = append(ts.XValues, t)
			ts.YValues = append(ts.YValues, float64(d.Count))
		}

		if !any {
			continue // no data for this period, do not include
		}

		series = append(series, ts)
	}

	graph := chart.Chart{
		Title:      "Hourly counts for " + day.Format("Mon Jan 2"),
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
			Name:           "Hour",
			NameStyle:      chart.StyleShow(),
			Style:          chart.StyleShow(),
			ValueFormatter: timeFormatter("3 PM"),
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

	var b bytes.Buffer
	graph.Render(chart.PNG, &b)

	return b.Bytes(), nil
}

func strokeDashArray(index int) []float64 {
	if index == 0 {
		return nil
	}
	return []float64{float64(index * 3), float64(index * 2)}
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
