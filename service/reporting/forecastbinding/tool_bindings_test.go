package forecastbinding

func syntheticToolBindings() ToolBindings {
	return ToolBindings{
		Profile:    "sample/EntityProfile",
		Conversion: "sample/NormalizeSelection",
		Data:       "sample/MetricSource",
	}
}
