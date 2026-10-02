package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/viant/agently-core/e2e/sdkcontract"
)

func main() {
	root := flag.String("workspace", "", "disposable workspace directory")
	reporting := flag.Bool("reporting", false, "enable browser reporting lifecycle and per-client mutable fixtures")
	ready := flag.String("ready", "", "path for localhost endpoint and fixture metadata")
	flag.Parse()
	if *root == "" || *ready == "" {
		fmt.Fprintln(os.Stderr, "-workspace and -ready are required")
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var fixture *sdkcontract.Server
	var err error
	if *reporting {
		fixture, err = sdkcontract.NewReporting(ctx, *root)
	} else {
		fixture, err = sdkcontract.New(ctx, *root)
	}
	must(err)
	defer fixture.Runtime.Close(context.Background())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	must(err)
	tokenFile := filepath.Join(*root, "fixture-owner.token")
	otherFile := filepath.Join(*root, "fixture-other.token")
	must(os.WriteFile(tokenFile, []byte(fixture.Token), 0600))
	must(os.WriteFile(otherFile, []byte(fixture.OtherToken), 0600))
	metadata := map[string]interface{}{"url": "http://" + listener.Addr().String(), "ownerTokenFile": tokenFile, "otherTokenFile": otherFile, "conversationId": sdkcontract.ConversationID, "queueConversationId": sdkcontract.QueueConversationID, "queueTurnId": "sdk-contract-queued-turn", "deleteConversationId": "sdk-contract-delete", "workspace": *root, "scope": "actual application API with native persistence, local fixture JWT and deterministic usage SSE"}
	if *reporting {
		snapshotsFile := filepath.Join(*root, "reporting-snapshots.json")
		snapshots, marshalErr := json.Marshal(sdkcontract.ReportingSnapshots())
		must(marshalErr)
		must(os.WriteFile(snapshotsFile, snapshots, 0600))
		metadata["reportingSnapshotsFile"] = snapshotsFile
		for _, client := range []string{"android", "swift", "ts"} {
			metadata[client+"ConversationId"] = "sdk-contract-" + client
			metadata[client+"TurnId"] = "sdk-contract-" + client + "-turn"
			metadata[client+"ArtifactId"] = fixture.ReportingArtifacts[client]
		}
	}
	body, err := json.MarshalIndent(metadata, "", "  ")
	must(err)
	must(os.WriteFile(*ready, body, 0600))
	server := &http.Server{Handler: fixture.Handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		var seq int64
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				seq++
				_ = fixture.Publish(ctx, seq)
			}
		}
	}()
	go func() {
		<-ctx.Done()
		stop, close := context.WithTimeout(context.Background(), 5*time.Second)
		defer close()
		_ = server.Shutdown(stop)
	}()
	fmt.Printf("SDK contract server ready at %s\n", metadata["url"])
	if err = server.Serve(listener); err != nil && err != http.ErrServerClosed {
		must(err)
	}
}
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
