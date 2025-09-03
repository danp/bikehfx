package main

type heatmapInput struct {
	Title          string                `json:"title"`
	XLabel         string                `json:"x_label"`
	YLabel         string                `json:"y_label"`
	XValues        []string              `json:"x_values"`
	CellWidth      float64               `json:"cell_width"`
	CellHeight     float64               `json:"cell_height"`
	Square         bool                  `json:"square"`
	Annotations    bool                  `json:"annotations"`
	XTicks         []heatmapInputTick    `json:"x_ticks,omitempty"`
	SortCounters   bool                  `json:"sort_counters"`
	XTickRotation  float64               `json:"x_tick_rotation,omitempty"`
	XTickFont      float64               `json:"x_tick_font,omitempty"`
	YTickFont      float64               `json:"y_tick_font,omitempty"`
	TitleFont      float64               `json:"title_font,omitempty"`
	AxisFont       float64               `json:"axis_font,omitempty"`
	AnnotationFont float64               `json:"annotation_font,omitempty"`
	Counters       []heatmapInputCounter `json:"counters"`
}

type heatmapInputCounter struct {
	Name    string              `json:"name"`
	Missing bool                `json:"missing"`
	Values  []heatmapInputValue `json:"values"`
}

type heatmapInputValue struct {
	X     string `json:"x"`
	Count int    `json:"count"`
}

type heatmapInputTick struct {
	Position int    `json:"position"`
	Label    string `json:"label"`
}
