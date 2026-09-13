// Command generate refreshes the shared web/native theme fixtures.
package main

import (
	"encoding/json"
	"os"

	"github.com/viant/agently-core/protocol/ui/theme"
)

func main() {
	source, err := os.ReadFile("testdata/baseline.yaml")
	check(err)
	manifest, err := theme.Parse(source)
	check(err)
	catalog, err := theme.Resolve(*manifest)
	check(err)
	data, err := json.MarshalIndent(catalog, "", "  ")
	check(err)
	css, err := theme.CSS(catalog)
	check(err)
	check(os.WriteFile("testdata/baseline.json", append(data, '\n'), 0644))
	check(os.WriteFile("testdata/baseline.css", []byte(css), 0644))
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
