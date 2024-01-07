package main

import (
	"sort"
	"strings"

	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

func postText(cs []counterSeries, records map[string]recordKind, headliner func(p *message.Printer, sum string) string) string {
	var sum int
	var csIndices []int
	for i, s := range cs {
		sum += s.series[0].val
		csIndices = append(csIndices, i)
	}
	sort.Slice(csIndices, func(i, j int) bool {
		return cs[csIndices[i]].series[0].val > cs[csIndices[j]].series[0].val
	})

	var out strings.Builder

	p := message.NewPrinter(language.English)

	sums := p.Sprintf("%d%s", sum, recordSymbol(records["sum"]))
	p.Fprintln(&out, headliner(p, sums))
	p.Fprintln(&out)

	for _, ci := range csIndices {
		s := cs[ci]
		p.Fprintf(&out, "%d%s %s\n", s.series[0].val, recordSymbol(records[s.counter.ID]), s.counter.Name)
	}

	recordKinds := make(map[recordKind]bool)
	for _, rk := range records {
		recordKinds[rk] = true
	}
	var recordKindKeys []recordKind
	for rk := range recordKinds {
		recordKindKeys = append(recordKindKeys, rk)
	}
	sort.Slice(recordKindKeys, func(i, j int) bool { return recordKindKeys[i] < recordKindKeys[j] })
	if len(recordKindKeys) > 0 {
		p.Fprintln(&out)
	}
	for _, rk := range recordKindKeys {
		p.Fprintln(&out, recordNote(rk))
	}

	return out.String()
}

type altTextGenerator struct {
	headlinePrinter func(p *message.Printer, len int) string
	changePrinter   func(p *message.Printer, cur int, pctChange int) string
}

func (a altTextGenerator) text(trvs []timeRangeValue) (string, error) {
	pr := message.NewPrinter(language.English)
	altText := a.headlinePrinter(pr, len(trvs))
	if len(trvs) > 1 {
		cur := float64(trvs[len(trvs)-1].val)
		prev := float64(trvs[len(trvs)-2].val)
		var pct int
		if cur > prev {
			pct = int((cur - prev) / prev * 100.0)
		} else {
			pct = -int((prev - cur) / prev * 100.0)
		}

		altText += " " + a.changePrinter(pr, int(cur), pct)
	}
	return altText, nil
}
