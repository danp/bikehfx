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

func TestGenerateCounterChartsSkipsHeatmapForInactiveCounter(t *testing.T) {
	t.Parallel()

	loc := time.UTC
	asOfEnd := time.Date(2024, 7, 2, 0, 0, 0, 0, loc)
	yearRange := timeRange{
		begin: time.Date(2024, 1, 1, 0, 0, 0, 0, loc),
		end:   asOfEnd,
	}
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
	charts, err := generateCounterCharts(context.Background(), pageDir, counter, asOfEnd, yearRange, trq)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := charts["year_heatmap"]; ok {
		t.Fatalf("year_heatmap chart generated for inactive counter: %v", charts)
	}
	if _, err := os.Stat(filepath.Join(pageDir, "heatmap.png")); !os.IsNotExist(err) {
		t.Fatalf("heatmap.png stat err = %v, want not exist", err)
	}
}
