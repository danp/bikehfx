package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"
	"github.com/hexops/valast"
	"gonum.org/v1/plot/cmpimg"
)

func TestDayPostText(t *testing.T) {
	t.Parallel()

	day := time.Date(2023, 7, 21, 0, 0, 0, 0, time.UTC)
	dayRange := newTimeRangeDate(day, 0, 0, 1)

	makeSeries := func(id, name string, dayValue int) counterSeries {
		var cs counterSeries
		cs.counter.ID = id
		cs.counter.Name = name
		if before, ok := strings.CutSuffix(name, " Short"); ok {
			cs.counter.ShortName = before
		}
		if dayValue >= 0 {
			cs.last = day
			cs.lastNonZero = day
			cs.series = append(cs.series, timeRangeValue{tr: dayRange, val: dayValue})
		}
		return cs
	}

	makeSeriesFull := func(id, name string, dayValue int, last, lastNonZero time.Time) counterSeries {
		cs := makeSeries(id, name, dayValue)
		cs.last = last
		cs.lastNonZero = lastNonZero
		return cs
	}

	t.Run("Complete", func(t *testing.T) {
		w := weather{
			max: 30.111, min: 20.222,
			rain: 1.234, snow: 2.345,
		}
		cs := []counterSeries{
			makeSeries("b", "Banana", 456),
			makeSeries("a", "Apple Short", 123),
			makeSeriesFull("d", "Dragon Fruit", 0, day.AddDate(0, -1, 0), time.Time{}),
			makeSeriesFull("c", "Coconut", 0, day.AddDate(0, -1, 0), day.AddDate(0, 0, 7)),
		}
		records := map[string]recordKind{
			"sum": recordKindAllTime,
			"a":   recordKindAllTime,
			"b":   recordKindYTD,
		}

		got := dayPostText(day, w, cs, records)
		expect(t, "text.txt", got)
	})

	t.Run("Minimal", func(t *testing.T) {
		cs := []counterSeries{
			makeSeries("a", "Apple", 123),
		}
		got := dayPostText(day, weather{}, cs, nil)
		expect(t, "text.txt", got)
	})
}

var unsafeNameRe = regexp.MustCompile(`[^-.\w/]+`)

func expect(t testing.TB, filename string, got any) {
	t.Helper()

	dir := t.Name()
	if unsafeNameRe.MatchString(dir) {
		var pathParts []string
		var curr string
		for i := 0; i < len(dir); i++ {
			if os.IsPathSeparator(dir[i]) {
				if curr != "" {
					pathParts = append(pathParts, curr)
					curr = ""
				}
			} else {
				curr += string(dir[i])
			}
		}
		if curr != "" {
			pathParts = append(pathParts, curr)
		}
		for i := 0; i < len(pathParts); i++ {
			if unsafeNameRe.MatchString(pathParts[i]) {
				safe := unsafeNameRe.ReplaceAllString(pathParts[i], "_")
				ts := sha256.New224()
				ts.Write([]byte(dir))
				safe += "_" + hex.EncodeToString(ts.Sum(nil))[:8]
				pathParts[i] = safe
			}
		}
		dir = filepath.Join(pathParts...)
	}
	path := filepath.Join("testdata", "expect", dir, filename)

	want, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		want = nil
	}

	var msg, gotString, wantString string
	var save []byte

	switch got := got.(type) {
	case []byte:
		if strings.HasSuffix(filename, ".png") {
			// Images differ slightly across GOARCHes due to floating point fuzziness.
			ok, err := cmpimg.EqualApprox("png", got, want, 0.05)
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				msg = fmt.Sprintf("%v: image path %v does not match expected", filename, path)
			}
		} else if !bytes.Equal(got, want) {
			msg = fmt.Sprintf("%v: path %v does not match expected", filename, path)
		}
		save = got
	case string:
		gotString = got + "\n"
		wantString = string(want)
	default:
		gotString = valast.String(got) + "\n"
		wantString = string(want)
	}

	if update() {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if save == nil {
			save = []byte(gotString)
		}
		if err := os.WriteFile(path, save, 0600); err != nil {
			t.Fatal(err)
		}
		return
	}

	if msg != "" {
		t.Fatal(msg)
	}
	if d := diff(gotString, wantString); d != "" {
		t.Fatalf("%v: path %v does not match expected\n%v", filename, path, d)
	}
}

//nolint:gochecknoinits
func init() {
	// For compatibility with other packages that also define an -update parameter, only define the
	// flag if it's not already defined.
	if updateFlag := flag.Lookup("update"); updateFlag == nil {
		flag.Bool("update", false, "update golden files, leaving unused)")
	}
}

func update() bool {
	return flag.Lookup("update").Value.(flag.Getter).Get().(bool)
}

func diff(got, want string) string {
	edits := myers.ComputeEdits(span.URIFromPath("out"), want, got)
	return fmt.Sprint(gotextdiff.ToUnified("want", "got", want, edits))
}
