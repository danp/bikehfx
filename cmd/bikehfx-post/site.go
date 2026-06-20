package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/danp/counterbase/directory"
	"github.com/graxinc/errutil"
	"github.com/peterbourgon/ff/v3/ffcli"
	"golang.org/x/sync/errgroup"
)

func newSiteCmd(rootConfig *rootConfig) *ffcli.Command {
	var (
		fs                = flag.NewFlagSet("bikehfx-post site", flag.ExitOnError)
		outputDir         = fs.String("output-dir", "", "directory to write Hugo counter page bundles into, such as content/bikehfxstats/counters")
		asOf              = fs.String("as-of", time.Now().AddDate(0, 0, -1).Format("20060102"), "inclusive latest day to include, in YYYYMMDD form")
		topN              = fs.Int("top-n", 10, "number of top rows to include per ranking table")
		writeSectionIndex = fs.Bool("write-section-index", false, "whether to write _index.md in the output directory")
	)

	return &ffcli.Command{
		Name:       "site",
		ShortUsage: "bikehfx-post site -output-dir <dir>",
		ShortHelp:  "generate Hugo-friendly static counter pages",
		FlagSet:    fs,
		Exec: func(ctx context.Context, args []string) error {
			if *outputDir == "" {
				return errutil.New(errutil.Tags{"flag": "output-dir", "msg": "required"})
			}

			loc, err := time.LoadLocation("America/Halifax")
			if err != nil {
				return errutil.With(err)
			}

			asOfDay, err := time.ParseInLocation("20060102", *asOf, loc)
			if err != nil {
				return errutil.With(err)
			}

			return generateSite(ctx, *outputDir, asOfDay, *topN, *writeSectionIndex, rootConfig.ccd, rootConfig.trq)
		},
	}
}

type siteCounterSummary struct {
	Name      string
	Slug      string
	TotalYear int
	TotalAll  int
	LastSeen  string
	Active    bool
}

type sitePageFrontMatter struct {
	Title           string                 `json:"title"`
	Type            string                 `json:"type,omitempty"`
	AsOf            string                 `json:"as_of"`
	CounterID       string                 `json:"counter_id,omitempty"`
	ShortName       string                 `json:"short_name,omitempty"`
	Active          bool                   `json:"active,omitempty"`
	Location        string                 `json:"location,omitempty"`
	LastSeen        string                 `json:"last_seen,omitempty"`
	LastNonZeroSeen string                 `json:"last_non_zero_seen,omitempty"`
	TotalYear       int                    `json:"total_year,omitempty"`
	TotalAllTime    int                    `json:"total_all_time,omitempty"`
	RecentDay       sitePeriodValue        `json:"recent_day,omitempty"`
	RecentSevenDays sitePeriodValue        `json:"recent_seven_days,omitempty"`
	MonthToDate     sitePeriodValue        `json:"month_to_date,omitempty"`
	TopDays         []siteRankRow          `json:"top_days,omitempty"`
	TopWeeks        []siteRankRow          `json:"top_weeks,omitempty"`
	TopMonths       []siteRankRow          `json:"top_months,omitempty"`
	YearHeatmaps    []siteYearHeatmapChart `json:"year_heatmaps,omitempty"`
	Charts          map[string]string      `json:"charts,omitempty"`
	StatusRows      []siteStatusRowFM      `json:"status_rows,omitempty"`
}

type siteStatusRowFM struct {
	Status     string `json:"status"`
	Counter    string `json:"counter"`
	CounterURL string `json:"counter_url"`
	Problem    string `json:"problem"`
	Since      string `json:"since,omitempty"`
	AgeDays    int    `json:"age_days"`
}

type siteRankRow struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

type sitePeriodValue struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

func generateSite(ctx context.Context, outputDir string, asOfDay time.Time, topN int, writeSectionIndex bool, ccd cyclingCounterDirectory, trq counterbaseTimeRangeQuerier) error {
	asOfDay = time.Date(asOfDay.Year(), asOfDay.Month(), asOfDay.Day(), 0, 0, 0, 0, asOfDay.Location())
	asOfEnd := asOfDay.AddDate(0, 0, 1)

	counters, err := ccd.counters(ctx, timeRange{end: asOfEnd})
	if err != nil {
		return errutil.With(err)
	}

	slices.SortFunc(counters, func(a, b directory.Counter) int {
		return strings.Compare(a.Name, b.Name)
	})

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return errutil.With(err)
	}

	summaries := make([]siteCounterSummary, len(counters))

	g, groupCtx := errgroup.WithContext(ctx)
	g.SetLimit(siteWorkerLimit(len(counters)))

	for i, counter := range counters {
		i := i
		counter := counter
		g.Go(func() error {
			summary, err := generateCounterPage(groupCtx, outputDir, asOfDay, asOfEnd, topN, counter, trq)
			if err != nil {
				return errutil.With(err)
			}
			summaries[i] = summary
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return errutil.With(err)
	}

	if writeSectionIndex {
		if err := writeSectionIndexPage(outputDir, asOfDay, summaries); err != nil {
			return errutil.With(err)
		}
	}

	if err := writeCounterStatusPage(ctx, outputDir, asOfDay, asOfEnd, counters, trq); err != nil {
		return errutil.With(err)
	}

	return nil
}

func siteWorkerLimit(numCounters int) int {
	if numCounters < 1 {
		return 1
	}

	limit := max(2, runtime.GOMAXPROCS(0))
	if limit > 8 {
		limit = 8
	}
	if limit > numCounters {
		limit = numCounters
	}
	return limit
}

func generateCounterPage(ctx context.Context, outputDir string, asOfDay, asOfEnd time.Time, topN int, counter directory.Counter, trq counterbaseTimeRangeQuerier) (siteCounterSummary, error) {
	slug := counterSlug(counter)
	pageDir := filepath.Join(outputDir, slug)
	if err := os.MkdirAll(pageDir, 0o755); err != nil {
		return siteCounterSummary{}, errutil.With(err)
	}

	allRange := counterCoverageRange(counter, asOfEnd)
	yearRange := newTimeRangeDate(time.Date(asOfDay.Year(), 1, 1, 0, 0, 0, 0, asOfDay.Location()), 1, 0, 0)
	if yearRange.end.After(asOfEnd) {
		yearRange.end = asOfEnd
	}

	allTRVs, err := trq.timeRangeValues(ctx, counter.ID, []timeRange{allRange})
	if err != nil {
		return siteCounterSummary{}, errutil.With(err)
	}
	yearTRVs, err := trq.timeRangeValues(ctx, counter.ID, []timeRange{yearRange})
	if err != nil {
		return siteCounterSummary{}, errutil.With(err)
	}

	recentDay, err := sitePeriodTotal(ctx, trq, counter.ID, timeRange{begin: asOfDay, end: asOfEnd}, asOfDay.Format("Jan 2"))
	if err != nil {
		return siteCounterSummary{}, errutil.With(err)
	}
	recentSevenDays, err := sitePeriodTotal(ctx, trq, counter.ID, timeRange{begin: asOfEnd.AddDate(0, 0, -7), end: asOfEnd}, "Trailing 7 days")
	if err != nil {
		return siteCounterSummary{}, errutil.With(err)
	}
	monthToDate, err := sitePeriodTotal(ctx, trq, counter.ID, timeRange{
		begin: time.Date(asOfDay.Year(), asOfDay.Month(), 1, 0, 0, 0, 0, asOfDay.Location()),
		end:   asOfEnd,
	}, asOfDay.Format("Jan")+" to date")
	if err != nil {
		return siteCounterSummary{}, errutil.With(err)
	}

	last, lastNonZero, _, err := trq.last(ctx, counter, asOfEnd, asOfDay)
	if err != nil {
		return siteCounterSummary{}, errutil.With(err)
	}

	topDays, err := topRanges(ctx, trq, counter, allRange.splitDate(0, 0, 1), topN, func(tr timeRange) string {
		return tr.begin.Format("2006-01-02")
	})
	if err != nil {
		return siteCounterSummary{}, errutil.With(err)
	}

	topWeeks, err := topRanges(ctx, trq, counter, weekRanges(allRange), topN, func(tr timeRange) string {
		return tr.begin.Format("2006-01-02")
	})
	if err != nil {
		return siteCounterSummary{}, errutil.With(err)
	}

	topMonths, err := topRanges(ctx, trq, counter, monthRanges(allRange), topN, func(tr timeRange) string {
		return tr.begin.Format("2006-01")
	})
	if err != nil {
		return siteCounterSummary{}, errutil.With(err)
	}

	yearHeatmaps, charts, err := generateCounterCharts(ctx, pageDir, counter, asOfEnd, trq)
	if err != nil {
		return siteCounterSummary{}, errutil.With(err)
	}
	yearHeatmapsNewestFirst := slices.Clone(yearHeatmaps)
	slices.Reverse(yearHeatmapsNewestFirst)

	fm := sitePageFrontMatter{
		Title:           counter.Name,
		Type:            "bikehfxstats-site",
		AsOf:            asOfDay.Format("2006-01-02"),
		CounterID:       counter.ID,
		ShortName:       counter.ShortName,
		Active:          counter.IsActive(),
		Location:        counter.Location.Text,
		LastSeen:        formatDate(last),
		LastNonZeroSeen: formatDate(lastNonZero),
		TotalYear:       trvSum(yearTRVs),
		TotalAllTime:    trvSum(allTRVs),
		RecentDay:       recentDay,
		RecentSevenDays: recentSevenDays,
		MonthToDate:     monthToDate,
		TopDays:         topDays,
		TopWeeks:        topWeeks,
		TopMonths:       topMonths,
		YearHeatmaps:    yearHeatmapsNewestFirst,
		Charts:          charts,
	}

	var body strings.Builder
	fmt.Fprintf(&body, "Data through %s.\n\n", asOfDay.Format("2006-01-02"))
	fmt.Fprintf(&body, "## Summary\n\n")
	fmt.Fprintf(&body, "- Total in %d: %d\n", asOfDay.Year(), fm.TotalYear)
	fmt.Fprintf(&body, "- Total all-time: %d\n", fm.TotalAllTime)
	fmt.Fprintf(&body, "- Active: %t\n", fm.Active)
	if fm.LastSeen != "" {
		fmt.Fprintf(&body, "- Last seen: %s\n", fm.LastSeen)
	}
	if fm.LastNonZeroSeen != "" {
		fmt.Fprintf(&body, "- Last non-zero count: %s\n", fm.LastNonZeroSeen)
	}
	if fm.Location != "" {
		fmt.Fprintf(&body, "- Location: %s\n", fm.Location)
	}

	if chart := charts["yearly_totals"]; chart != "" {
		fmt.Fprintf(&body, "\n## Yearly Totals\n\n![Yearly totals](%s)\n", chart)
	}
	if chart := charts["recent_weekly"]; chart != "" {
		fmt.Fprintf(&body, "\n## Recent Weekly Trend\n\n![Recent weekly trend](%s)\n", chart)
	}
	for _, heatmap := range yearHeatmapsNewestFirst {
		fmt.Fprintf(&body, "\n## %d Daily Heatmap\n\n![%d daily heatmap](%s)\n", heatmap.Year, heatmap.Year, heatmap.Filename)
	}

	appendRankTable(&body, "Top Days", topDays, "Day")
	appendRankTable(&body, "Top Weeks", topWeeks, "Week Starting")
	appendRankTable(&body, "Top Months", topMonths, "Month")

	if err := writeMarkdownPage(filepath.Join(pageDir, "index.md"), fm, body.String()); err != nil {
		return siteCounterSummary{}, errutil.With(err)
	}

	return siteCounterSummary{
		Name:      counter.Name,
		Slug:      slug,
		TotalYear: fm.TotalYear,
		TotalAll:  fm.TotalAllTime,
		LastSeen:  fm.LastSeen,
		Active:    fm.Active,
	}, nil
}

func sitePeriodTotal(ctx context.Context, trq counterbaseTimeRangeQuerier, counterID string, tr timeRange, label string) (sitePeriodValue, error) {
	trvs, err := trq.timeRangeValues(ctx, counterID, []timeRange{tr})
	if err != nil {
		return sitePeriodValue{}, errutil.With(err)
	}
	return sitePeriodValue{Label: label, Count: trvSum(trvs)}, nil
}

type siteYearHeatmapChart struct {
	Year     int    `json:"year"`
	Filename string `json:"filename"`
}

func generateCounterCharts(ctx context.Context, pageDir string, counter directory.Counter, asOfEnd time.Time, trq counterbaseTimeRangeQuerier) ([]siteYearHeatmapChart, map[string]string, error) {
	charts := make(map[string]string)

	allRange := counterCoverageRange(counter, asOfEnd)
	years := yearRanges(allRange)
	yearTRVs, err := trq.timeRangeValues(ctx, counter.ID, years)
	if err != nil {
		return nil, nil, errutil.With(err)
	}
	if trvSum(yearTRVs) > 0 {
		img, err := timeRangeBarGraph(yearTRVs, fmt.Sprintf("Yearly totals for %s", counter.Name), func(tr timeRange) string {
			return tr.begin.Format("2006")
		})
		if err != nil {
			return nil, nil, errutil.With(err)
		}
		filename := "count-by-year.png"
		if err := os.WriteFile(filepath.Join(pageDir, filename), img, 0o644); err != nil {
			return nil, nil, errutil.With(err)
		}
		charts["yearly_totals"] = filename
	}

	weeklyHistory, err := recentWeeklyHistory(ctx, trq, counter, allRange.end)
	if err != nil {
		return nil, nil, errutil.With(err)
	}
	if len(weeklyHistory) > 0 {
		img, err := yearWeekChart(weeklyHistory, fmt.Sprintf("Recent weekly totals for %s", counter.Name))
		if err != nil {
			return nil, nil, errutil.With(err)
		}
		filename := "count-by-week-recent-years.png"
		if err := os.WriteFile(filepath.Join(pageDir, filename), img, 0o644); err != nil {
			return nil, nil, errutil.With(err)
		}
		charts["recent_weekly"] = filename
	}

	var heatmaps []siteYearHeatmapChart
	for _, heatmapRange := range yearlyHeatmapRanges(allRange) {
		dayCounts, err := dayCountsForRange(ctx, trq, counter.ID, heatmapRange)
		if err != nil {
			return nil, nil, errutil.With(err)
		}
		if positiveDayCountSum(dayCounts) > 0 {
			displayRange := calendarYearRange(heatmapRange.begin)
			axis := newYearHeatmapAxis(displayRange)
			img, _, err := buildYearCounterHeatmap(ctx, counter, displayRange, axis, dayCounts)
			if err != nil {
				return nil, nil, errutil.With(err)
			}
			if len(img) > 0 {
				year := heatmapRange.begin.Year()
				filename := fmt.Sprintf("heatmap-%d.png", year)
				if err := os.WriteFile(filepath.Join(pageDir, filename), img, 0o644); err != nil {
					return nil, nil, errutil.With(err)
				}
				charts[fmt.Sprintf("year_heatmap_%d", year)] = filename
				heatmaps = append(heatmaps, siteYearHeatmapChart{Year: year, Filename: filename})
			}
		}
	}

	return heatmaps, charts, nil
}

func topRanges(ctx context.Context, trq counterbaseTimeRangeQuerier, counter directory.Counter, trs []timeRange, topN int, label func(timeRange) string) ([]siteRankRow, error) {
	trvs, err := trq.timeRangeValues(ctx, counter.ID, trs)
	if err != nil {
		return nil, errutil.With(err)
	}

	slices.SortFunc(trvs, func(a, b timeRangeValue) int {
		if a.val != b.val {
			if a.val > b.val {
				return -1
			}
			return 1
		}
		return b.tr.begin.Compare(a.tr.begin)
	})

	var rows []siteRankRow
	for _, trv := range trvs {
		if trv.val == 0 {
			continue
		}
		rows = append(rows, siteRankRow{Label: label(trv.tr), Count: trv.val})
		if len(rows) == topN {
			break
		}
	}
	return rows, nil
}

func recentWeeklyHistory(ctx context.Context, trq counterbaseTimeRangeQuerier, counter directory.Counter, asOfEnd time.Time) (map[int]map[int]timeRangeValue, error) {
	begin := asOfEnd.AddDate(-3, 0, 0)
	for begin.Weekday() != time.Sunday {
		begin = begin.AddDate(0, 0, -1)
	}
	end := asOfEnd
	for end.Weekday() != time.Sunday {
		end = end.AddDate(0, 0, -1)
	}
	if !begin.Before(end) {
		return nil, nil
	}

	trs := timeRange{begin: begin, end: end}.splitDate(0, 0, 7)
	trvs, err := trq.timeRangeValues(ctx, counter.ID, trs)
	if err != nil {
		return nil, errutil.With(err)
	}

	out := make(map[int]map[int]timeRangeValue)
	for _, trv := range trvs {
		year, week := trv.tr.end.ISOWeek()
		if out[year] == nil {
			out[year] = make(map[int]timeRangeValue)
		}
		out[year][week] = trv
	}

	for year, weeks := range out {
		var any bool
		for _, trv := range weeks {
			if trv.val > 0 {
				any = true
				break
			}
		}
		if !any {
			delete(out, year)
		}
	}

	return out, nil
}

func dayCountsForRange(ctx context.Context, trq counterbaseTimeRangeQuerier, counterID string, tr timeRange) (map[time.Time]int, error) {
	trvs, err := trq.timeRangeValues(ctx, counterID, tr.splitDate(0, 0, 1))
	if err != nil {
		return nil, errutil.With(err)
	}
	out := make(map[time.Time]int, len(trvs))
	for _, trv := range trvs {
		out[trv.tr.begin] = trv.val
	}
	return out, nil
}

func positiveDayCountSum(counts map[time.Time]int) int {
	var out int
	for _, count := range counts {
		if count > 0 {
			out += count
		}
	}
	return out
}

func writeSectionIndexPage(outputDir string, asOfDay time.Time, summaries []siteCounterSummary) error {
	slices.SortFunc(summaries, func(a, b siteCounterSummary) int {
		return strings.Compare(a.Name, b.Name)
	})

	fm := sitePageFrontMatter{
		Title: "BikeHfx Counters",
		Type:  "bikehfxstats-site",
		AsOf:  asOfDay.Format("2006-01-02"),
	}

	var body strings.Builder
	var activeCount int
	for _, summary := range summaries {
		if summary.Active {
			activeCount++
		}
	}
	fmt.Fprintf(&body, "Per-counter bike counter pages generated through %s.\n\n", asOfDay.Format("2006-01-02"))
	fmt.Fprintf(&body, "%d counters are included here, with %d currently active.\n", len(summaries), activeCount)

	return writeMarkdownPage(filepath.Join(outputDir, "_index.md"), fm, body.String())
}

type siteCounterStatusRow struct {
	Name    string
	Slug    string
	Status  string
	Problem string
	Since   time.Time
	AgeDays int
}

func writeCounterStatusPage(ctx context.Context, outputDir string, asOfDay, asOfEnd time.Time, counters []directory.Counter, trq counterbaseTimeRangeQuerier) error {
	rows := make([]siteCounterStatusRow, 0, len(counters))
	for _, counter := range counters {
		if !counter.IsActive() {
			continue
		}
		row, err := counterStatusRow(ctx, counter, asOfDay, asOfEnd, trq)
		if err != nil {
			return errutil.With(err)
		}
		rows = append(rows, row)
	}

	slices.SortFunc(rows, func(a, b siteCounterStatusRow) int {
		if c := statusRank(a.Status) - statusRank(b.Status); c != 0 {
			return c
		}
		if a.Status != "green" && a.AgeDays != b.AgeDays {
			return b.AgeDays - a.AgeDays
		}
		return strings.Compare(a.Name, b.Name)
	})

	fm := sitePageFrontMatter{
		Title:      "Counter Status",
		Type:       "bikehfxstats-status",
		AsOf:       asOfDay.Format("2006-01-02"),
		StatusRows: statusRowsFrontMatter(rows),
	}

	var body strings.Builder
	fmt.Fprintf(&body, "Active counter status generated through %s.", asOfDay.Format("2006-01-02"))

	return writeMarkdownPage(filepath.Join(outputDir, "status", "_index.md"), fm, body.String())
}

func statusRowsFrontMatter(rows []siteCounterStatusRow) []siteStatusRowFM {
	out := make([]siteStatusRowFM, 0, len(rows))
	for _, row := range rows {
		out = append(out, siteStatusRowFM{
			Status:     row.Status,
			Counter:    row.Name,
			CounterURL: "../" + row.Slug + "/",
			Problem:    row.Problem,
			Since:      formatDate(row.Since),
			AgeDays:    row.AgeDays,
		})
	}
	return out
}

func counterStatusRow(ctx context.Context, counter directory.Counter, asOfDay, asOfEnd time.Time, trq counterbaseTimeRangeQuerier) (siteCounterStatusRow, error) {
	last, lastNonZero, status, err := trq.last(ctx, counter, asOfEnd, asOfDay)
	if err != nil {
		return siteCounterStatusRow{}, errutil.With(err)
	}

	row := siteCounterStatusRow{
		Name:   counter.Name,
		Slug:   counterSlug(counter),
		Status: siteStatusName(status),
	}

	switch status {
	case counterDataStatusMissing:
		row.Problem = counterMissingProblem(last, lastNonZero, asOfDay)
		row.Since = counterLastStatusTime(counterSeries{last: last, lastNonZero: lastNonZero})
	case counterDataStatusPartial:
		problem, since, err := counterPartialProblem(ctx, counter, asOfDay, asOfEnd, trq)
		if err != nil {
			return siteCounterStatusRow{}, errutil.With(err)
		}
		row.Problem = problem
		row.Since = since
	default:
		row.Problem = "OK"
		row.Since = last
	}

	row.AgeDays = calendarDaysBetween(row.Since, asOfDay)
	return row, nil
}

func counterPartialProblem(ctx context.Context, counter directory.Counter, asOfDay, asOfEnd time.Time, trq counterbaseTimeRangeQuerier) (string, time.Time, error) {
	type directionProblem struct {
		text  string
		since time.Time
	}
	var problems []directionProblem
	for _, dir := range counter.Directions {
		last, lastNonZero, err := trq.directionLast(ctx, counter.ID, dir.ID, asOfEnd)
		if err != nil {
			return "", time.Time{}, errutil.With(err)
		}
		dirName := dir.Name
		if dirName == "" {
			dirName = dir.ID
		}
		if last.IsZero() || last.Before(asOfDay) {
			problems = append(problems, directionProblem{text: "No " + dirName + " data", since: last})
			continue
		}
		if lastNonZero.IsZero() || lastNonZero.Before(asOfDay) {
			problems = append(problems, directionProblem{text: "No positive " + dirName + " counts", since: lastNonZero})
		}
	}
	if len(problems) == 0 {
		return "partial data", time.Time{}, nil
	}
	slices.SortFunc(problems, func(a, b directionProblem) int {
		if a.since.IsZero() && !b.since.IsZero() {
			return -1
		}
		if !a.since.IsZero() && b.since.IsZero() {
			return 1
		}
		return a.since.Compare(b.since)
	})
	texts := make([]string, 0, len(problems))
	for _, problem := range problems {
		texts = append(texts, problem.text)
	}
	return strings.Join(texts, "; "), problems[0].since, nil
}

func counterMissingProblem(last, lastNonZero, asOfDay time.Time) string {
	if last.IsZero() {
		return "No data"
	}
	if last.Before(asOfDay) {
		return "No data"
	}
	if lastNonZero.IsZero() || lastNonZero.Before(asOfDay) {
		return "No positive counts"
	}
	return "Missing data"
}

func siteStatusName(status counterDataStatus) string {
	switch status {
	case counterDataStatusMissing:
		return "red"
	case counterDataStatusPartial:
		return "yellow"
	default:
		return "green"
	}
}

func statusRank(status string) int {
	switch status {
	case "red":
		return 0
	case "yellow":
		return 1
	default:
		return 2
	}
}

func calendarDaysBetween(begin, end time.Time) int {
	if begin.IsZero() || end.IsZero() {
		return 0
	}
	beginDate := time.Date(begin.Year(), begin.Month(), begin.Day(), 0, 0, 0, 0, end.Location())
	endDate := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, end.Location())
	var days int
	for d := beginDate; d.Before(endDate); d = d.AddDate(0, 0, 1) {
		days++
	}
	return days
}

func writeMarkdownPage(path string, fm sitePageFrontMatter, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return errutil.With(err)
	}

	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(fm); err != nil {
		return errutil.With(err)
	}
	out.WriteString("\n")
	out.WriteString(strings.TrimSpace(body))
	out.WriteString("\n")

	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		return errutil.With(err)
	}
	return nil
}

func appendRankTable(body *strings.Builder, heading string, rows []siteRankRow, label string) {
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(body, "\n## %s\n\n", heading)
	fmt.Fprintf(body, "| %s | Count |\n", label)
	body.WriteString("|---|---:|\n")
	for _, row := range rows {
		fmt.Fprintf(body, "| %s | %d |\n", row.Label, row.Count)
	}
}

func counterCoverageRange(counter directory.Counter, asOfEnd time.Time) timeRange {
	begin := asOfEnd
	end := asOfEnd
	var latestEnd time.Time
	for _, sr := range counter.ServiceRanges {
		if sr.Start.IsZero() {
			continue
		}
		if sr.Start.Before(begin) {
			begin = sr.Start.Time
		}
		if sr.End.IsZero() {
			latestEnd = asOfEnd
			continue
		}
		if sr.End.After(latestEnd) {
			latestEnd = sr.End.Time
		}
	}
	if begin.Equal(asOfEnd) {
		begin = time.Date(asOfEnd.Year(), 1, 1, 0, 0, 0, 0, asOfEnd.Location())
	}
	if !latestEnd.IsZero() && latestEnd.Before(end) {
		end = latestEnd
	}
	if end.Before(begin) {
		end = begin
	}
	return timeRange{begin: begin, end: end}
}

func yearlyHeatmapRanges(tr timeRange) []timeRange {
	if !tr.begin.Before(tr.end) {
		return nil
	}

	var out []timeRange
	loc := tr.begin.Location()
	for year := tr.begin.Year(); year <= tr.end.AddDate(0, 0, -1).Year(); year++ {
		begin := time.Date(year, 1, 1, 0, 0, 0, 0, loc)
		end := begin.AddDate(1, 0, 0)
		if begin.Before(tr.begin) {
			begin = tr.begin
		}
		if end.After(tr.end) {
			end = tr.end
		}
		if begin.Before(end) {
			out = append(out, timeRange{begin: begin, end: end})
		}
	}
	return out
}

func calendarYearRange(t time.Time) timeRange {
	begin := time.Date(t.Year(), 1, 1, 0, 0, 0, 0, t.Location())
	return timeRange{begin: begin, end: begin.AddDate(1, 0, 0)}
}

func weekRanges(tr timeRange) []timeRange {
	begin := tr.begin
	for begin.Weekday() != time.Sunday {
		begin = begin.AddDate(0, 0, -1)
	}
	return timeRange{begin: begin, end: tr.end}.splitDate(0, 0, 7)
}

func monthRanges(tr timeRange) []timeRange {
	begin := time.Date(tr.begin.Year(), tr.begin.Month(), 1, 0, 0, 0, 0, tr.begin.Location())
	return timeRange{begin: begin, end: tr.end}.splitDate(0, 1, 0)
}

func yearRanges(tr timeRange) []timeRange {
	begin := time.Date(tr.begin.Year(), 1, 1, 0, 0, 0, 0, tr.begin.Location())
	end := tr.end
	if begin.Equal(end) {
		return nil
	}
	return timeRange{begin: begin, end: end}.splitDate(1, 0, 0)
}

func counterSlug(counter directory.Counter) string {
	src := counter.ID
	if src == "" {
		src = counterName(counter)
	}
	src = strings.ToLower(src)

	var out strings.Builder
	lastDash := true
	for _, r := range src {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			out.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			out.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(out.String(), "-")
	if slug == "" {
		return "counter"
	}
	return slug
}

func formatDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}
