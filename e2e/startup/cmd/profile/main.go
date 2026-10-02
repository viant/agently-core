//go:build cgo

// Command profile measures native runtime initialization independently of
// process launch, schema provisioning, application setup, and the first query.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"sort"
	"sync"
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/datly/bootstrap/connector"
)

func main() {
	root := flag.String("root", ".", "Core source root (for source-backed bootstrap)")
	n := flag.Int("n", 5, "runtime initializations")
	profile := flag.String("cpuprofile", "", "optional CPU profile output file")
	metadata := flag.String("metadata", "", "optional final runtime metadata JSON output (outside timed startup)")
	flag.Parse()
	if *n < 1 {
		fmt.Fprintln(os.Stderr, "-n must be positive")
		os.Exit(2)
	}
	absoluteRoot, err := filepath.Abs(*root)
	must(err)
	var mu sync.Mutex
	actions := map[string]int{}
	connections := 0
	sql.Register("startup_sqlite3", &sqlite3.SQLiteDriver{ConnectHook: func(c *sqlite3.SQLiteConn) error {
		mu.Lock()
		connections++
		mu.Unlock()
		c.RegisterAuthorizer(func(op int, a, b, c string) int {
			mu.Lock()
			actions[fmt.Sprintf("op=%d arg1=%s arg2=%s db=%s", op, a, b, c)]++
			mu.Unlock()
			return sqlite3.SQLITE_OK
		})
		return nil
	}})
	if *profile != "" {
		f, err := os.Create(*profile)
		must(err)
		must(pprof.StartCPUProfile(f))
		defer f.Close()
		defer pprof.StopCPUProfile()
	}
	times := []float64{}
	for i := 0; i < *n; i++ {
		start := time.Now()
		server, err := native.New(context.Background(), native.Options{SourceRoot: absoluteRoot, Connectors: []connector.Config{{Name: "agently", Driver: "startup_sqlite3", DSN: ":memory:", MaxOpenConns: 1, MaxIdleConns: 1}}})
		elapsed := float64(time.Since(start).Nanoseconds()) / 1e6
		must(err)
		times = append(times, elapsed)
		if i == *n-1 && *metadata != "" {
			value, err := server.Metadata(context.Background())
			must(err)
			encoded, err := json.MarshalIndent(value, "", "  ")
			must(err)
			must(os.WriteFile(*metadata, encoded, 0600))
		}
		must(server.Shutdown(context.Background()))
	}
	sorted := append([]float64{}, times...)
	sort.Float64s(sorted)
	median := sorted[len(sorted)/2]
	if len(sorted)%2 == 0 {
		median = (sorted[len(sorted)/2-1] + median) / 2
	}
	must(json.NewEncoder(os.Stdout).Encode(map[string]any{
		"boundary":   "native.New entry to return; shutdown excluded; empty SQLite database, no schema or data provisioning",
		"startup_ms": times, "median_ms": median, "connections": connections,
		"sqlite_authorizer_actions": actions, "cpu_profile": *profile,
		"profile_scope": "CPU profile includes the initialization/shutdown loop; timings exclude shutdown",
	}))
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
