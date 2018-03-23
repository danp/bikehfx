package ecocounter

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestGetDatapoints(t *testing.T) {
	var (
		begin = time.Unix(1521504000, 0)
		end   = begin
	)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Method, "GET"; got != want {
			t.Errorf("got method %q, want %q", got, want)
		}

		if got, want := r.URL.Path, "/api/cw6Xk4jW4X4R/data/periode/123"; got != want {
			t.Errorf("got path %q, want %q", got, want)
		}

		wantValues := url.Values{
			"begin": []string{begin.Format(requestDateFormat)},
			"end":   []string{end.Format(requestDateFormat)},
			"step":  []string{"2"},
		}
		if got := r.URL.Query(); !reflect.DeepEqual(got, wantValues) {
			t.Errorf("got query values\n%+v\nwant\n%+v", got, wantValues)
		}

		f, err := os.Open("testdata/hourly.json")
		if err != nil {
			t.Error(err)
			return
		}
		defer f.Close()

		io.Copy(w, f)
	}))
	defer ts.Close()

	cl := Client{
		BaseURL: ts.URL,
	}

	ds, err := cl.GetDatapoints("123", begin, end, ResolutionHour)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := len(ds), 24; got != want {
		t.Fatalf("got %d datapoints, want %d\nds = %+v", got, want, ds)
	}

	if got, want := ds[15].Time, "2018-03-20 15:00:00"; got != want {
		t.Errorf("got ds[15].Time %q, want %q", got, want)
	}

	if got, want := ds[15].Count, 9; got != want {
		t.Errorf("got ds[15].Count %d, want %d", got, want)
	}
}

// Verify results with comptage=null don't get returned.
func TestGetDatapoints_NoData(t *testing.T) {
	var (
		begin = time.Unix(1521504000, 0)
		end   = begin
	)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"date":"2018-03-20 00:45:00","comptage":null,"timestamp":1521503100000}]`))
	}))
	defer ts.Close()

	cl := Client{
		BaseURL: ts.URL,
	}

	ds, err := cl.GetDatapoints("123", begin, end, ResolutionHour)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := len(ds), 0; got != want {
		t.Fatalf("got %d datapoints, want %d", got, want)
	}
}

func ExampleClient_GetDatapoints() {
	var (
		cl    Client
		begin = time.Unix(1521504000, 0) // 2018-03-20
		end   = begin
	)

	// 100036476 is the Halifax University Ave Arts Centre counter,
	// on the web at http://www.eco-public.com/public2/?id=100036476.
	ds, err := cl.GetDatapoints("100036476", begin, end, ResolutionHour)
	if err != nil {
		panic(err)
	}

	for _, d := range ds {
		fmt.Println("for hour", d.Time, "there were", d.Count, "bike trips counted")
	}
}

func TestGetNonPublicDatapoints(t *testing.T) {
	var (
		begin = time.Unix(1521504000, 0)
		end   = begin
	)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Method, "POST"; got != want {
			t.Errorf("got method %q, want %q", got, want)
		}

		if got, want := r.URL.Path, "/ParcPublic/CounterData"; got != want {
			t.Errorf("got path %q, want %q", got, want)
		}

		if err := r.ParseForm(); err != nil {
			t.Error(err)
			return
		}

		wantValues := url.Values{
			"debut":       []string{begin.Format(nonPublicRequestDateFormat)},
			"fin":         []string{end.Format(nonPublicRequestDateFormat)},
			"idOrganisme": []string{"org"},
			"pratiques":   []string{"dir1;dir2"},
			"interval":    []string{"3"},
		}
		if got := r.Form; !reflect.DeepEqual(got, wantValues) {
			t.Errorf("got query values\n%+v\nwant\n%+v", got, wantValues)
		}

		w.Write([]byte(`[["03\/20\/2018","245.0"],["03\/21\/2018","255.0"],["03\/22\/2018","222.0"],222.0]`))
	}))
	defer ts.Close()

	cl := Client{
		BaseURL: ts.URL,
	}

	ds, err := cl.GetNonPublicDatapoints("org", []string{"dir1", "dir2"}, begin, end)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := len(ds), 3; got != want {
		t.Fatalf("got %d datapoints, want %d\nds = %+v", got, want, ds)
	}

	if got, want := ds[1].Time, "2018-03-21 00:00:00"; got != want {
		t.Errorf("got ds[1].Time %q, want %q", got, want)
	}

	if got, want := ds[1].Count, 255; got != want {
		t.Errorf("got ds[1].Count %d, want %d", got, want)
	}
}

func ExampleClient_GetNonPublicDatapoints() {
	var (
		cl    Client
		begin = time.Unix(1521504000, 0) // 2018-03-20
		end   = begin
	)

	// Fetch datapoints for the South Park Street counter, listed at
	// http://www.eco-public.com/ParcPublic/?id=4638 but doesn't have its
	// own page.
	ds, err := cl.GetNonPublicDatapoints("4638", []string{"101039526", "102039526"}, begin, end)
	if err != nil {
		panic(err)
	}

	for _, d := range ds {
		fmt.Println("for hour", d.Time, "there were", d.Count, "bike trips counted")
	}
}
