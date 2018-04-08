package main

import (
	"bytes"
	"image/color"
	"time"

	"github.com/danp/bikehfx/ecocounter"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"
)

func makeHourlyGraph(cl *ecocounter.Client, day time.Time) ([]byte, error) {
	p, err := plot.New()
	if err != nil {
		return nil, err
	}
	p.Title.Text = "Hourly counts for " + day.Format("Mon Jan 2")
	p.X.Tick.Marker = hourTicker(day)
	p.X.Label.Text = "Hour"
	p.Y.Min = 0
	p.Y.Label.Text = "Count"
	p.Legend.Top = true
	p.Legend.Left = true

	p.Add(plotter.NewGrid())

	for i, c := range publicCounters {
		ds, err := cl.GetDatapoints(c.ecoID, day, day, ecocounter.ResolutionHour)
		if err != nil {
			return nil, err
		}

		var (
			data plotter.XYs
			any  bool
		)
		for _, d := range ds {
			t, err := time.ParseInLocation("2006-01-02 15:04:05", d.Time, time.Local)
			if err != nil {
				return nil, err
			}

			if d.Count > 0 {
				any = true
			}

			var xy struct {
				X, Y float64
			}
			xy.X = float64(t.Hour())
			xy.Y = float64(d.Count)

			data = append(data, xy)
		}

		if !any {
			continue // no data for this period, do not include
		}

		ln, _, err := plotter.NewLinePoints(data)
		if err != nil {
			return nil, err
		}

		ln.Color = lineColor(i)
		ln.Dashes = strokeDashArray(i)

		p.Add(ln)                  // , pts)
		p.Legend.Add(c.name(), ln) // , pts)
	}

	wt, err := p.WriterTo(28*vg.Centimeter, 10*vg.Centimeter, "png")
	if err != nil {
		return nil, err
	}

	var b bytes.Buffer
	if _, err := wt.WriteTo(&b); err != nil {
		return nil, err
	}

	return b.Bytes(), nil
}

var colors = []color.Color{
	color.RGBA{R: 0, G: 116, B: 217, A: 255}, // blue
	color.RGBA{R: 0, G: 217, B: 210, A: 255}, // cyan
	color.RGBA{R: 0, G: 217, B: 101, A: 255}, // green
	color.RGBA{R: 217, G: 0, B: 116, A: 255}, // red
	color.RGBA{R: 217, G: 101, B: 0, A: 255}, // orange
}

func lineColor(index int) color.Color {
	return colors[index%len(colors)]
}

func strokeDashArray(index int) []vg.Length {
	if index == 0 {
		return nil
	}
	return []vg.Length{vg.Length(index * 3), vg.Length(index * 2)}
}

type hourTicker time.Time

func (h hourTicker) Ticks(min, max float64) []plot.Tick {
	var ts []plot.Tick

	for i := 0; i < 24; i++ {
		t := plot.Tick{
			Value: float64(i),
		}
		if i%2 == 0 {
			var tt time.Time
			tt = tt.Add(time.Duration(i) * time.Hour)
			t.Label = tt.Format("3 PM")
		}
		ts = append(ts, t)
	}

	return ts
}
