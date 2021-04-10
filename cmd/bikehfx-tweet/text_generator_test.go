package main

import (
	"testing"

	"github.com/danp/counterbase/directory"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/text/message"
)

func TestTweetText(t *testing.T) {
	cs := []counterSeries{
		{
			counter: directory.Counter{ID: "uni-arts", Name: "Uni Arts"},
			series:  []timeRangeValue{{val: 111}},
		},
		{
			counter: directory.Counter{ID: "vernon", Name: "Vernon"},
			series:  []timeRangeValue{{val: 222}},
		},
		{
			counter: directory.Counter{ID: "south-park", Name: "South Park"},
			series:  []timeRangeValue{{val: 555}},
		},
	}

	got := tweetText(cs, nil, func(p *message.Printer, sum string) string {
		return p.Sprintf("the sum is %s", sum)
	})
	want := `the sum is 888

555 South Park
222 Vernon
111 Uni Arts
`

	if d := cmp.Diff(want, got); d != "" {
		t.Error(d)
	}
}

func TestTweetTextFormatsThousands(t *testing.T) {
	cs := []counterSeries{
		{
			counter: directory.Counter{ID: "uni-arts", Name: "Uni Arts"},
			series:  []timeRangeValue{{val: 1111}},
		},
		{
			counter: directory.Counter{ID: "vernon", Name: "Vernon"},
			series:  []timeRangeValue{{val: 2222}},
		},
		{
			counter: directory.Counter{ID: "south-park", Name: "South Park"},
			series:  []timeRangeValue{{val: 5555}},
		},
	}

	got := tweetText(cs, nil, func(p *message.Printer, sum string) string {
		return p.Sprintf("the sum is %s", sum)
	})
	want := `the sum is 8,888

5,555 South Park
2,222 Vernon
1,111 Uni Arts
`

	if d := cmp.Diff(want, got); d != "" {
		t.Error(d)
	}
}

func TestTweetTextRecords(t *testing.T) {
	cs := []counterSeries{
		{
			counter: directory.Counter{ID: "uni-arts", Name: "Uni Arts"},
			series:  []timeRangeValue{{val: 111}},
		},
		{
			counter: directory.Counter{ID: "vernon", Name: "Vernon"},
			series:  []timeRangeValue{{val: 222}},
		},
		{
			counter: directory.Counter{ID: "south-park", Name: "South Park"},
			series:  []timeRangeValue{{val: 555}},
		},
	}

	records := map[string]recordKind{
		"vernon":   recordKindAllTime,
		"uni-arts": recordKindYTD,
		"sum":      recordKindAllTime,
	}

	got := tweetText(cs, records, func(p *message.Printer, sum string) string {
		return p.Sprintf("the sum is %s", sum)
	})
	want := `the sum is 888**

555 South Park
222** Vernon
111* Uni Arts

** all-time record
* year-to-date record
`

	if d := cmp.Diff(want, got); d != "" {
		t.Error(d)
	}
}

func TestAltTextGenerator(t *testing.T) {
	hp := func(p *message.Printer, len int) string {
		return p.Sprintf("len=%d", len)
	}
	cp := func(p *message.Printer, cur int, pctChange int) string {
		return p.Sprintf("cur=%d pctChange=%d", cur, pctChange)
	}

	cases := []struct {
		name string
		trvs []timeRangeValue
		want string
	}{
		{
			name: "Increase",
			trvs: []timeRangeValue{{val: 10}, {val: 15}},
			want: "len=2 cur=15 pctChange=50",
		},
		{
			name: "Decrease",
			trvs: []timeRangeValue{{val: 15}, {val: 10}},
			want: "len=2 cur=10 pctChange=-33",
		},
		{
			name: "Same",
			trvs: []timeRangeValue{{val: 15}, {val: 15}},
			want: "len=2 cur=15 pctChange=0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := altTextGenerator{headlinePrinter: hp, changePrinter: cp}

			txt, err := g.text(tc.trvs)
			if err != nil {
				t.Fatal(err)
			}

			if d := cmp.Diff(tc.want, txt); d != "" {
				t.Error(d)
			}
		})
	}
}
