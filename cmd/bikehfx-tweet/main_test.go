package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type fakeTimeRangeQuerier struct {
	R map[string][]timeRangeValue
}

func (f fakeTimeRangeQuerier) query(ctx context.Context, counterID string, trs []timeRange) ([]timeRangeValue, error) {
	key := counterID + ": " + timeRangeString(trs)

	res, ok := f.R[key]
	if !ok {
		return nil, fmt.Errorf("no results for key: %s", key)
	}
	if len(res) == 0 {
		return res, nil
	}

	if vl := len(res); vl != 0 && vl != len(trs) {
		return nil, fmt.Errorf("fakeTimeRangeQuerier: %s: got %d result Values but %d timeRanges given", counterID, len(res), len(trs))
	}
	return res, nil
}

func timeRangeString(trs []timeRange) string {
	var out []string
	for _, tr := range trs {
		out = append(out, fmt.Sprintf("%s to %s", tr.begin.Format(time.RFC3339), tr.end.Format(time.RFC3339)))
	}
	return strings.Join(out, ", ")
}

func writeFileFromReader(file string, r io.Reader) error {
	f, err := os.Create(file)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := io.Copy(f, r); err != nil {
		return err
	}

	return f.Close()
}
