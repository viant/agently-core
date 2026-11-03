package workspace

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNavigationIconSVGSurvivesLayoutProjection(t *testing.T) {
	const iconSVG = `{"viewBox":[0,0,20,20],"paths":["M2 3L7 11L16 4"],"rects":[{"x":1,"y":2,"width":17,"height":15,"rx":2}],"fill":"none","stroke":"currentColor"}`
	for _, presentation := range []string{"", "sections"} {
		for _, children := range []string{"children", "menus", "items"} {
			t.Run(presentation+"/"+children, func(t *testing.T) {
				var layout Layout
				input := `{"version":1,"id":"example","left":{"navigation":{"presentation":"` + presentation + `"}},"applications":[{"id":"example","title":"Example","menus":[{"id":"group","title":"Group","iconSvg":` + iconSVG + `,"` + children + `":[{"id":"drawing","label":"Drawing","disabled":true,"iconSvg":` + iconSVG + `},{"id":"standard","title":"Standard","disabled":true,"icon":"home"}]}]}]}`
				require.NoError(t, json.Unmarshal([]byte(input), &layout))
				require.NoError(t, validateLayout(&layout))
				projected, err := filterLayoutContextWithRuntimes(context.Background(), &layout, nil, nil)
				require.NoError(t, err)
				require.Equal(t, presentation, projected.Left.Navigation.Presentation)
				encoded, err := json.Marshal(projected.Applications[0].Menus[0])
				require.NoError(t, err)
				require.JSONEq(t, `{"id":"group","title":"Group","iconSvg":`+iconSVG+`,"children":[{"id":"drawing","title":"Drawing","disabled":true,"iconSvg":`+iconSVG+`},{"id":"standard","title":"Standard","disabled":true,"icon":"home"}]}`, string(encoded))
			})
		}
	}
}
