package tests

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLinkedGoalExecutableStartsWithPrivateComponents(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	binary := filepath.Join(t.TempDir(), "agently-datly")
	build := exec.Command("go", "build", "-o", binary, "./cmd/datly")
	build.Dir = project
	build.Env = append(os.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("linked executable: %v\n%s", err, output)
	}
	_, dbPath := goalFixture(t, project)
	configDir := t.TempDir()
	deps := filepath.Join(configDir, "connections")
	must(t, os.Mkdir(deps, 0700))
	connectors := map[string]any{"Connectors": []map[string]any{{"Name": "agently", "Driver": "sqlite3", "DSN": dbPath + "?_foreign_keys=on"}}}
	data, err := json.Marshal(connectors)
	must(t, err)
	must(t, os.WriteFile(filepath.Join(deps, "agently.json"), data, 0600))
	config := map[string]any{"BaseDir": project, "Connector": "agently", "DependencyURL": deps, "Endpoint": map[string]any{"Address": "127.0.0.1:0"}, "GoBootstrap": map[string]any{"Packages": []string{"github.com/viant/agently-core/internal/datly/goal/..."}}}
	data, err = json.Marshal(config)
	must(t, err)
	configPath := filepath.Join(configDir, "datly.json")
	must(t, os.WriteFile(configPath, data, 0600))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	command := exec.CommandContext(ctx, binary, "run", "-conf", configPath)
	command.Dir = project
	stdout, err := command.StdoutPipe()
	must(t, err)
	var diagnostics bytes.Buffer
	command.Stderr = &diagnostics
	must(t, command.Start())
	done := make(chan struct{})
	var processErr error
	go func() { processErr = command.Wait(); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	address := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "HTTP listening on ") {
				address <- strings.TrimPrefix(line, "HTTP listening on ")
				return
			}
		}
	}()
	var actualAddress string
	select {
	case actualAddress = <-address:
	case <-done:
		t.Fatalf("startup: %v\n%s", processErr, diagnostics.String())
	case <-ctx.Done():
		<-done
		t.Fatalf("startup timeout: %v\n%s", processErr, diagnostics.String())
	}
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get("http://" + actualAddress + "/v1/api/agently/goal/c1")
	must(t, err)
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("private persistence exposed from released binary: %d", response.StatusCode)
	}
}
