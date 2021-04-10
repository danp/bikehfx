package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/danp/counterbase/directory"
	"github.com/google/go-cmp/cmp"
)

func TestDailyGraph(t *testing.T) {
	loc, err := time.LoadLocation("America/Halifax")
	if err != nil {
		t.Fatal(err)
	}

	dayHours := newTimeRangeDate(time.Date(2021, 3, 26, 0, 0, 0, 0, loc), 0, 0, 1).split(time.Hour)

	hours := func(name string, vals ...int) counterSeries {
		if l := len(vals); l != 24 {
			panic(fmt.Sprintf("hours called with %d values, want 24", l))
		}
		var trvs []timeRangeValue
		for i, dh := range dayHours {
			trvs = append(trvs, timeRangeValue{tr: dh, val: vals[i]})
		}
		return counterSeries{counter: directory.Counter{Name: name}, series: trvs}
	}

	run := func(t *testing.T, cs []counterSeries, outFilename string) {
		img, err := dailyGraph(dayHours[0].begin, cs)
		if err != nil {
			t.Fatal(err)
		}
		defer img.Close()

		if err := writeFileFromReader("testdata/daily_graph_"+outFilename+".png", img); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("Base", func(t *testing.T) {
		series := []counterSeries{
			hours("Vernon", 21, 23, 25, 27, 25, 23, 21, 23, 25, 27, 25, 23, 21, 23, 25, 27, 25, 23, 21, 23, 25, 27, 25, 23),
			hours("South Park", 1, 3, 5, 7, 5, 3, 1, 3, 5, 7, 5, 3, 1, 3, 5, 7, 5, 3, 1, 3, 5, 7, 5, 3),
			hours("Uni Arts", 11, 13, 15, 17, 15, 13, 11, 13, 15, 17, 15, 13, 11, 13, 15, 17, 15, 13, 11, 13, 15, 17, 15, 13),
		}

		run(t, series, "base")
	})

	t.Run("StartsAtFirstNonZeroHour", func(t *testing.T) {
		series := []counterSeries{
			hours("Vernon", 0, 0, 0, 0, 0, 23, 21, 23, 25, 27, 25, 23, 21, 23, 25, 27, 25, 23, 21, 23, 25, 27, 25, 23),
			hours("South Park", 0, 0, 0, 0, 0, 0, 1, 3, 5, 7, 5, 3, 1, 3, 5, 7, 5, 3, 1, 3, 5, 7, 5, 3),
			hours("Uni Arts", 0, 0, 0, 0, 0, 0, 11, 13, 15, 17, 15, 13, 11, 13, 15, 17, 15, 13, 11, 13, 15, 17, 15, 13),
		}

		run(t, series, "starts_at_first_non_zero_hour")
	})

	t.Run("ConsistentLineStyles", func(t *testing.T) {
		series := []counterSeries{
			hours("Vernon", 21, 23, 25, 27, 25, 23, 21, 23, 25, 27, 25, 23, 21, 23, 25, 27, 25, 23, 21, 23, 25, 27, 25, 23),
			hours("South Park", 1, 3, 5, 7, 5, 3, 1, 3, 5, 7, 5, 3, 1, 3, 5, 7, 5, 3, 1, 3, 5, 7, 5, 3),
			hours("Almon", 11, 13, 15, 17, 15, 13, 11, 13, 15, 17, 15, 13, 11, 13, 15, 17, 15, 13, 11, 13, 15, 17, 15, 13),
		}

		run(t, series, "consistent_line_styles")
	})

	t.Run("SortsNames", func(t *testing.T) {
		series := []counterSeries{
			hours("Vernon", 21, 23, 25, 27, 25, 23, 21, 23, 25, 27, 25, 23, 21, 23, 25, 27, 25, 23, 21, 23, 25, 27, 25, 23),
			hours("South Park", 1, 3, 5, 7, 5, 3, 1, 3, 5, 7, 5, 3, 1, 3, 5, 7, 5, 3, 1, 3, 5, 7, 5, 3),
			hours("Almon", 11, 13, 15, 17, 15, 13, 11, 13, 15, 17, 15, 13, 11, 13, 15, 17, 15, 13, 11, 13, 15, 17, 15, 13),
		}

		run(t, series, "sorts_names")
	})
}

func TestDailyAltText(t *testing.T) {
	loc, err := time.LoadLocation("America/Halifax")
	if err != nil {
		t.Fatal(err)
	}

	dayHours := newTimeRangeDate(time.Date(2021, 3, 26, 0, 0, 0, 0, loc), 0, 0, 1).split(time.Hour)

	hours := func(name string, vals ...int) counterSeries {
		if l := len(vals); l != 24 {
			panic(fmt.Sprintf("hours called with %d values, want 24", l))
		}
		var trvs []timeRangeValue
		for i, dh := range dayHours {
			trvs = append(trvs, timeRangeValue{tr: dh, val: vals[i]})
		}
		return counterSeries{counter: directory.Counter{Name: name}, series: trvs}
	}

	run := func(t *testing.T, series []counterSeries, want string) {
		got := dailyAltText(series)
		if d := cmp.Diff(want, got); d != "" {
			t.Error(d)
		}
	}

	t.Run("SingleHighest", func(t *testing.T) {
		series := []counterSeries{
			hours("Vernon", 21, 23, 25, 27, 25, 23, 21, 23, 25, 27, 85, 23, 21, 23, 25, 27, 25, 23, 21, 23, 25, 27, 25, 23),
			hours("South Park", 1, 3, 5, 7, 5, 3, 1, 3, 5, 7, 5, 3, 1, 3, 5, 7, 5, 3, 1, 3, 5, 7, 5, 3),
			hours("Uni Arts", 11, 13, 15, 17, 15, 13, 11, 13, 15, 17, 15, 13, 11, 13, 15, 17, 15, 13, 11, 13, 15, 17, 15, 13),
		}

		want := `Line chart of bike trips by hour from the South Park, Uni Arts, and Vernon counters. ` +
			`The highest hourly count was 85 during the 10 AM hour from the Vernon counter.`
		run(t, series, want)
	})

	t.Run("MultiHighestSingleCounter", func(t *testing.T) {
		series := []counterSeries{
			hours("Vernon", 21, 23, 25, 27, 25, 23, 21, 23, 25, 27, 27, 23, 21, 23, 25, 27, 25, 23, 21, 23, 25, 27, 25, 23),
			hours("South Park", 1, 3, 5, 7, 5, 3, 1, 3, 5, 7, 5, 3, 1, 3, 5, 7, 5, 3, 1, 3, 5, 7, 5, 3),
			hours("Uni Arts", 11, 13, 15, 17, 15, 13, 11, 13, 15, 17, 15, 13, 11, 13, 15, 17, 15, 13, 11, 13, 15, 17, 15, 13),
		}

		want := `Line chart of bike trips by hour from the South Park, Uni Arts, and Vernon counters. ` +
			`The highest hourly count was 27 from the Vernon counter.`
		run(t, series, want)
	})

	t.Run("MultiHighestMultiCounter", func(t *testing.T) {
		series := []counterSeries{
			hours("Vernon", 21, 23, 25, 27, 25, 23, 21, 23, 25, 85, 25, 23, 21, 23, 25, 27, 25, 23, 21, 23, 25, 27, 25, 23),
			hours("South Park", 1, 3, 5, 7, 5, 3, 1, 3, 5, 7, 5, 85, 27, 3, 5, 7, 5, 3, 1, 3, 5, 7, 5, 3),
			hours("Uni Arts", 11, 13, 15, 17, 15, 13, 11, 13, 15, 17, 15, 13, 11, 13, 15, 17, 15, 13, 11, 13, 15, 17, 15, 13),
		}

		want := `Line chart of bike trips by hour from the South Park, Uni Arts, and Vernon counters. ` +
			`The highest hourly count was 85 from the South Park and Vernon counters.`
		run(t, series, want)
	})

	t.Run("OneCounter", func(t *testing.T) {
		series := []counterSeries{
			hours("Vernon", 21, 23, 25, 27, 25, 23, 21, 23, 25, 85, 25, 23, 21, 23, 25, 27, 25, 23, 21, 23, 25, 27, 25, 23),
		}

		want := `Line chart of bike trips by hour from the Vernon counter. ` +
			`The highest hourly count was 85 during the 9 AM hour from the Vernon counter.`
		run(t, series, want)
	})
}
