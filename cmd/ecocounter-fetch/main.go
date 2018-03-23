package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"time"

	"github.com/danp/bikehfx/ecocounter"
)

func main() {
	var (
		id    = flag.String("counter", "", "counter id")
		begin = flag.String("begin", "", "begin date (YYYYMMDD)")
		end   = flag.String("end", "", "end date (YYYYMMDD)")
		res   = flag.String("resolution", "day", "resolution")
	)

	flag.Parse()

	if *id == "" {
		log.Fatal("need counter")
	}

	bd := parseDate("begin", *begin)
	ed := parseDate("end", *end)

	var cres ecocounter.Resolution
	switch *res {
	case "day":
		cres = ecocounter.ResolutionDay
	case "hour":
		cres = ecocounter.ResolutionHour
	case "15m":
		cres = ecocounter.ResolutionFifteenMinute
	default:
		log.Fatalf("unknown resolution %q, try day or hour", *res)
	}

	var cl ecocounter.Client
	ds, err := cl.GetDatapoints(*id, bd, ed, cres)
	if err != nil {
		log.Fatal(err)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(ds); err != nil {
		log.Fatal(err)
	}
}

func parseDate(name, s string) time.Time {
	if s == "" {
		log.Fatalln("need", name, "date")
	}

	t, err := time.Parse("20060102", s)
	if err != nil {
		log.Fatalln("unable to parse", name, "date:", err)
	}

	return t
}
