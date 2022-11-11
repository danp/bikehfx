package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/danp/counterbase/directory"
)

type recordWidth int

const (
	recordWidthDay   recordWidth = 1
	recordWidthWeek  recordWidth = 2
	recordWidthMonth recordWidth = 3
	recordWidthYear  recordWidth = 4
)

type recordKind int

const (
	recordKindAllTime recordKind = 1
	recordKindYTD     recordKind = 2
)

type recordsChecker interface {
	check(ctx context.Context, before time.Time, currentValues []counterSeries, width recordWidth) (map[string]recordKind, error)
}

type counterbaseRecordsChecker struct {
	qu  Querier
	ccd cyclingCounterDirectory
}

func (r counterbaseRecordsChecker) check(ctx context.Context, before time.Time, currentValues []counterSeries, width recordWidth) (map[string]recordKind, error) {
	boy := time.Date(before.Year(), 1, 1, 0, 0, 0, 0, before.Location())

	recordRanges := map[recordKind]timeRange{
		recordKindAllTime: {end: before},
	}
	recordRangeOrder := []recordKind{recordKindAllTime}
	if width != recordWidthYear {
		recordRanges[recordKindYTD] = timeRange{begin: boy, end: before}
		recordRangeOrder = append(recordRangeOrder, recordKindYTD)
	}
	records := make(map[string]recordKind)

	for _, c := range currentValues {
		for _, rk := range recordRangeOrder {
			if _, ok := records[c.counter.ID]; ok {
				break
			}
			rr := recordRanges[rk]
			is, err := isRecordForCounters(ctx, r.qu, []directory.Counter{c.counter}, width, rr, c.series[0].val)
			if err != nil {
				return nil, err
			}
			if is {
				records[c.counter.ID] = rk
			}
		}
	}

	var csSum int
	for _, c := range currentValues {
		csSum += c.series[0].val
	}

	for _, rk := range recordRangeOrder {
		if _, ok := records["sum"]; ok {
			break
		}
		rr := recordRanges[rk]

		counters, err := r.ccd.counters(ctx, rr)
		if err != nil {
			return nil, err
		}

		is, err := isRecordForCounters(ctx, r.qu, counters, width, rr, csSum)
		if err != nil {
			return nil, err
		}
		if is {
			records["sum"] = rk
		}
	}

	return records, nil
}

func isRecordForCounters(ctx context.Context, qu Querier, counters []directory.Counter, width recordWidth, lookback timeRange, val int) (bool, error) {
	var quotedCounterIDs []string
	for _, c := range counters {
		quotedCounterIDs = append(quotedCounterIDs, "'"+c.ID+"'")
	}

	var modifiers []string
	switch width {
	case recordWidthDay:
	case recordWidthWeek:
		modifiers = append(modifiers, "strftime('-%w days',time,'unixepoch','localtime')")
	case recordWidthMonth:
		modifiers = append(modifiers, "'start of month'")
	case recordWidthYear:
		modifiers = append(modifiers, "'start of year'")
	default:
		return false, fmt.Errorf("unsupported width %d", width)
	}

	q := `select cast(strftime('%s', date(time,'unixepoch','localtime'`
	if len(modifiers) > 0 {
		q += `,` + strings.Join(modifiers, ",")
	}
	q += `)) as integer) as time, sum(value) from counter_data where `

	conds := []string{"counter_id in (" + strings.Join(quotedCounterIDs, ",") + ")"}
	if !lookback.begin.IsZero() {
		conds = append(conds, `date(time,'unixepoch','localtime') >= '`+lookback.begin.Format("2006-01-02")+`'`)
	}
	if !lookback.end.IsZero() {
		conds = append(conds, `date(time,'unixepoch','localtime') < '`+lookback.end.Format("2006-01-02")+`'`)
	}
	q += strings.Join(conds, " and ")
	q += ` group by 1 order by 2 desc limit 1`

	mat, err := qu.Query(ctx, q)
	if err != nil {
		return false, err
	}
	if len(mat) == 0 || len(mat[0].Values) == 0 {
		return true, nil
	}

	return int(mat[0].Values[0].Value) < val, nil
}
