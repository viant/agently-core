// schema initializes a disposable local discovery fixture, never a product DB.
package main

import (
	"database/sql"
	_ "embed"
	"fmt"
	_ "modernc.org/sqlite"
	"os"
)

//go:embed schema.sql
var ddl string

func main() {
	if len(os.Args) != 2 {
		panic("usage: schema <new SQLite file>")
	}
	path := os.Args[1]
	if _, err := os.Stat(path); err == nil {
		panic("refusing to overwrite existing database")
	} else if !os.IsNotExist(err) {
		panic(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		panic(err)
	}
	defer db.Close()
	if _, err = db.Exec(ddl); err != nil {
		panic(err)
	}
	fmt.Println("initialized disposable Agently discovery schema")
}
