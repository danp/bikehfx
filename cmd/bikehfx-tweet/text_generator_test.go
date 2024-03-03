package main

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/text/message"
)

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
