package main

import (
	"strings"
	"testing"
	"time"
)

func TestWeeklyPostText(t *testing.T) {
	t.Parallel()

	week := time.Date(2023, 7, 23, 0, 0, 0, 0, time.UTC)
	weekRange := newTimeRangeDate(week, 0, 0, 7)

	makeSeries := func(id, name string, weekValue int) counterSeriesV2 {
		var cs counterSeriesV2
		cs.counter.ID = id
		cs.counter.Name = name
		if before, ok := strings.CutSuffix(name, " Short"); ok {
			cs.counter.ShortName = before
		}
		if weekValue >= 0 {
			cs.last = weekRange.end
			cs.lastNonZero = weekRange.end
			cs.series = append(cs.series, timeRangeValue{tr: weekRange, val: weekValue})
		}
		return cs
	}

	makeSeriesFull := func(id, name string, dayValue int, last, lastNonZero time.Time) counterSeriesV2 {
		cs := makeSeries(id, name, dayValue)
		cs.last = last
		cs.lastNonZero = lastNonZero
		return cs
	}

	t.Run("Complete", func(t *testing.T) {
		cs := []counterSeriesV2{
			makeSeries("b", "Banana", 456),
			makeSeries("a", "Apple Short", 123),
			makeSeriesFull("d", "Dragon Fruit", 0, week.AddDate(0, -1, 0), time.Time{}),
			makeSeriesFull("c", "Coconut", 0, week.AddDate(0, -1, 0), week.AddDate(0, 0, 7)),
			makeSeriesFull("e", "Eggplant", 1, week.AddDate(0, 0, 3), week.AddDate(0, 0, 3)),
		}
		records := map[string]recordKind{
			"sum": recordKindAllTime,
			"a":   recordKindAllTime,
			"b":   recordKindYTD,
		}

		got := weekPostText(weekRange, cs, records)
		expect(t, "text.txt", got)
	})

	t.Run("Minimal", func(t *testing.T) {
		cs := []counterSeriesV2{
			makeSeries("a", "Apple", 123),
		}
		got := weekPostText(weekRange, cs, nil)
		expect(t, "text.txt", got)
	})
}
