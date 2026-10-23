// Command generate refreshes the shared web/native theme fixtures.
package main

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"

	"github.com/viant/agently-core/protocol/ui/theme"
)

func main() {
	manifestPath := flag.String("manifest", "testdata/baseline.yaml", "manifest to validate and render")
	output := flag.String("output", "testdata/baseline", "output path without extension")
	flag.Parse()
	source, err := os.ReadFile(*manifestPath)
	check(err)
	manifest, err := theme.Parse(source)
	check(err)
	catalog, err := theme.Resolve(*manifest)
	check(err)
	data, err := json.MarshalIndent(catalog, "", "  ")
	check(err)
	css, err := theme.CSS(catalog)
	check(err)
	check(os.MkdirAll(filepath.Dir(*output), 0755))
	check(os.WriteFile(*output+".json", append(data, '\n'), 0644))
	check(os.WriteFile(*output+".css", []byte(css), 0644))
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
