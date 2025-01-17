package main

import (
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

type altTextGenerator struct {
	headlinePrinter func(p *message.Printer, len int) string
	changePrinter   func(p *message.Printer, cur int, pctChange int) string
}

func (a altTextGenerator) text(trvs []timeRangeValue) (string, error) {
	pr := message.NewPrinter(language.English)
	var nonZeroLen int
	for _, trv := range trvs {
		if trv.val != 0 {
			nonZeroLen++
		}
	}
	altText := a.headlinePrinter(pr, nonZeroLen)
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
