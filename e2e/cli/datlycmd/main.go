package main

import (
	"fmt"
	"os"

	_ "github.com/viant/agently-core/pkg/dependency"
	"github.com/viant/datly"
	datlycmd "github.com/viant/datly/cmd"
)

func main() {
	if err := datlycmd.RunApp(datly.Version, os.Args[1:]); err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
}
