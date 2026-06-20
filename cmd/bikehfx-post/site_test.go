package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/danp/counterbase/directory"
	"github.com/danp/counterbase/query"
)

type emptySiteQuerier struct{}

func (emptySiteQuerier) Query(context.Context, string) ([]query.Point, error) {
	return nil, nil
}

func TestGenerateCounterChartsSkipsHeatmapWithoutData(t *testing.T) {
	t.Parallel()

	loc := time.UTC
	asOfEnd := time.Date(2024, 7, 2, 0, 0, 0, 0, loc)
	counter := directory.Counter{
		ID:   "historical",
		Name: "Historical Counter",
		ServiceRanges: []directory.ServiceRange{
			{
				Start: directory.SD(time.Date(2020, 1, 1, 0, 0, 0, 0, loc)),
				End:   directory.SD(time.Date(2023, 1, 1, 0, 0, 0, 0, loc)),
			},
		},
	}
	trq := counterbaseTimeRangeQuerier{querier: emptySiteQuerier{}}

	pageDir := t.TempDir()
	heatmaps, charts, err := generateCounterCharts(context.Background(), pageDir, counter, asOfEnd, trq)
	if err != nil {
		t.Fatal(err)
	}
	if len(heatmaps) != 0 {
		t.Fatalf("heatmaps = %v, want none", heatmaps)
	}
	if _, ok := charts["year_heatmap_2020"]; ok {
		t.Fatalf("year_heatmap_2020 chart generated without data: %v", charts)
	}
	if _, err := os.Stat(filepath.Join(pageDir, "heatmap-2020.png")); !os.IsNotExist(err) {
		t.Fatalf("heatmap-2020.png stat err = %v, want not exist", err)
	}
}

func TestCounterCoverageRangeArchivedUsesServiceEnd(t *testing.T) {
	t.Parallel()

	loc := time.UTC
	asOfEnd := time.Date(2024, 7, 2, 0, 0, 0, 0, loc)
	counter := directory.Counter{
		ServiceRanges: []directory.ServiceRange{
			{
				Start: directory.SD(time.Date(2020, 1, 1, 0, 0, 0, 0, loc)),
				End:   directory.SD(time.Date(2023, 1, 1, 0, 0, 0, 0, loc)),
			},
		},
	}

	got := counterCoverageRange(counter, asOfEnd)
	if want := time.Date(2020, 1, 1, 0, 0, 0, 0, loc); !got.begin.Equal(want) {
		t.Fatalf("begin = %v, want %v", got.begin, want)
	}
	if want := time.Date(2023, 1, 1, 0, 0, 0, 0, loc); !got.end.Equal(want) {
		t.Fatalf("end = %v, want %v", got.end, want)
	}
}

func TestYearlyHeatmapRanges(t *testing.T) {
	t.Parallel()

	loc := time.UTC
	got := yearlyHeatmapRanges(timeRange{
		begin: time.Date(2020, 7, 8, 0, 0, 0, 0, loc),
		end:   time.Date(2022, 3, 4, 0, 0, 0, 0, loc),
	})

	want := []timeRange{
		{
			begin: time.Date(2020, 7, 8, 0, 0, 0, 0, loc),
			end:   time.Date(2021, 1, 1, 0, 0, 0, 0, loc),
		},
		{
			begin: time.Date(2021, 1, 1, 0, 0, 0, 0, loc),
			end:   time.Date(2022, 1, 1, 0, 0, 0, 0, loc),
		},
		{
			begin: time.Date(2022, 1, 1, 0, 0, 0, 0, loc),
			end:   time.Date(2022, 3, 4, 0, 0, 0, 0, loc),
		},
	}
	if len(got) != len(want) {
		t.Fatalf("len(yearlyHeatmapRanges) = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if !got[i].begin.Equal(want[i].begin) || !got[i].end.Equal(want[i].end) {
			t.Fatalf("range %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestCalendarYearRange(t *testing.T) {
	t.Parallel()

	loc := time.UTC
	got := calendarYearRange(time.Date(2018, 8, 28, 0, 0, 0, 0, loc))
	if want := time.Date(2018, 1, 1, 0, 0, 0, 0, loc); !got.begin.Equal(want) {
		t.Fatalf("begin = %v, want %v", got.begin, want)
	}
	if want := time.Date(2019, 1, 1, 0, 0, 0, 0, loc); !got.end.Equal(want) {
		t.Fatalf("end = %v, want %v", got.end, want)
	}
}
