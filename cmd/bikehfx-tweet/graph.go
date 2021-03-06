package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"sort"
	"time"

	"github.com/danp/bikehfx/ecocounter"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/plotutil"
	"gonum.org/v1/plot/vg"
)

func makeHourlyGraph(day time.Time, counters []counter) ([]byte, error) {
	if err := initGraph(); err != nil {
		return nil, err
	}
	plotutil.DefaultColors = plotutil.DarkColors

	p, err := plot.New()
	if err != nil {
		return nil, err
	}
	p.Title.Text = "Counts for " + day.Format("Mon Jan 2") + " by hour starting"
	p.X.Tick.Marker = hourTicker(day)
	p.Y.Min = 0
	p.Y.Label.Text = "Count"
	p.Legend.Top = true

	grid := plotter.NewGrid()
	grid.Vertical.Color = color.Gray{175}
	grid.Horizontal.Color = color.Gray{175}
	p.Add(grid)

	type counterXYs struct {
		c   *counter
		xys plotter.XYs
	}
	var cxys []counterXYs

	for _, c := range counters {
		c := c
		ds, err := c.querier.query(day, ecocounter.ResolutionHour)
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

			// Skip adding 0s until the first hour with a count >0.
			// This cuts down on 0s for the early hours of the day.
			if d.Count == 0 && !any {
				continue
			}
			any = true

			var xy struct {
				X, Y float64
			}
			xy.X = float64(t.Hour())
			xy.Y = float64(d.Count)

			data = append(data, xy)
		}

		if !any {
			continue // no data for this day, do not include
		}

		sort.Slice(data, func(i, j int) bool { return data[i].X < data[j].X })

		cxys = append(cxys, counterXYs{
			c:   &c,
			xys: data,
		})
	}

	earliestHour := math.MaxFloat64
	for _, d := range cxys {
		for _, xy := range d.xys {
			if xy.X < earliestHour {
				earliestHour = xy.X
			}
		}
	}
	for ci, d := range cxys {
		for i := int(earliestHour); i < int(d.xys[0].X); i++ {
			var xy struct {
				X, Y float64
			}
			xy.X = float64(i)
			xy.Y = float64(0)
			d.xys = append(d.xys, xy)
		}
		sort.Slice(d.xys, func(i, j int) bool { return d.xys[i].X < d.xys[j].X })

		ln, err := plotter.NewLine(d.xys)
		if err != nil {
			return nil, err
		}

		ln.LineStyle.Color = plotutil.Color(ci)
		ln.LineStyle.Dashes = plotutil.Dashes(ci)
		ln.LineStyle.Width = vg.Points(2)

		p.Add(ln)
		p.Legend.Add(d.c.name, ln)
	}

	wt, err := p.WriterTo(20*vg.Centimeter, 10*vg.Centimeter, "png")
	if err != nil {
		return nil, err
	}

	var b bytes.Buffer
	if _, err := wt.WriteTo(&b); err != nil {
		return nil, err
	}

	img, _, err := image.Decode(&b)
	if err != nil {
		return nil, err
	}

	bnds := img.Bounds()
	const padding = 20
	outRect := image.Rect(bnds.Min.X-padding, bnds.Min.Y-padding, bnds.Max.X+padding, bnds.Max.Y+padding)
	out := image.NewRGBA(outRect)
	draw.Draw(out, out.Bounds(), &image.Uniform{color.RGBA{255, 255, 255, 255}}, image.Point{}, draw.Src)
	draw.Draw(out, img.Bounds(), img, outRect.Min.Add(image.Pt(padding, padding)), draw.Over)

	b.Reset()
	if err := png.Encode(&b, out); err != nil {
		return nil, err
	}

	return b.Bytes(), nil
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
			t.Label = tt.Format("3PM")
		}
		ts = append(ts, t)
	}

	return ts
}
