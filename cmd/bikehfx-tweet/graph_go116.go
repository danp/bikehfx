// +build go1.16

package main

import (
	_ "embed"

	"github.com/golang/freetype/truetype"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"
)

//go:embed Arial.ttf
var arial []byte

func initGraph() error {
	fontTTF, err := truetype.Parse(arial)
	if err != nil {
		return err
	}
	const fontName = "Arial"
	vg.AddFont(fontName, fontTTF)
	plot.DefaultFont = fontName
	plotter.DefaultFont = fontName
	return nil
}
