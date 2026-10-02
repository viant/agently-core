package tests

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	rawread "github.com/viant/agently-core/internal/datly/payload/read"
	binaryread "github.com/viant/agently-core/internal/datly/payload/reference"
	payloadwrite "github.com/viant/agently-core/internal/datly/payload/write"
	"github.com/viant/bindly/locator"
	requestprovider "github.com/viant/bindly/provider/request"
	"github.com/viant/bindly/resource"
	"github.com/viant/datly/bootstrap"
	dexec "github.com/viant/datly/exec"
	druntime "github.com/viant/datly/runtime"
	writer "github.com/viant/datly/runtime/handler/writer"
	"github.com/viant/datly/runtime/registry"
	"github.com/viant/datly/spec"
	dsql "github.com/viant/datly/sql"
	"github.com/viant/datly/sql/dml"
	viewprovider "github.com/viant/datly/sql/reader/provider"
	dtag "github.com/viant/datly/tag"
)

type payloadProbeRequest struct {
	Component string
	DBPath    string
	Body      string
	Filters   map[string]json.RawMessage
	Raw       bool
}

func TestPayloadLegacyV1Parity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := os.Getenv("LEGACY_GOAL_PROBE")
	if legacy == "" {
		legacy = legacyProbeBinary(t, project)
	}
	type input struct {
		body    string
		filters map[string]any
	}
	type expected struct {
		failed  bool
		rows    map[string]map[string]any
		rawRows map[string]map[string]any
	}
	type useCase struct {
		desc   string
		input  input
		expect expected
	}
	encoded := func(value string) string { return base64.StdEncoding.EncodeToString([]byte(value)) }
	wireBytes := func(value string) []int {
		result := make([]int, len(value))
		for index, b := range []byte(value) {
			result[index] = int(b)
		}
		return result
	}
	body := func(id, value string) string {
		raw, err := json.Marshal(map[string]any{"data": []map[string]any{{"id": id, "tenantId": "tenant-a", "kind": "request", "mimeType": "text/plain", "sizeBytes": len(value), "storage": "inline", "inlineBody": wireBytes(value)}}})
		must(t, err)
		return string(raw)
	}
	seed := map[string]any{"kind": "request", "mimetype": "text/plain", "sizebytes": float64(9), "storage": "inline", "inlinebody": encoded("seed body"), "compression": "none", "digest": "seed-digest", "redacted": float64(1)}
	defaultFilters := map[string]any{"tenantID": "tenant-a", "ids": []string{"p1", "p2"}}
	cases := []useCase{
		{desc: "tenant predicate excludes a foreign tenant and missing identity", input: input{filters: map[string]any{"tenantID": "tenant-a", "ids": []string{"p1", "p-foreign", "absent"}}}, expect: expected{rows: map[string]map[string]any{"p1": seed}}},
		{desc: "optional tenant path is absent for an internal ID lookup", input: input{filters: map[string]any{"ids": []string{"p1"}}}, expect: expected{rows: map[string]map[string]any{"p1": seed}}},
		{desc: "empty reader result", input: input{filters: map[string]any{"tenantID": "tenant-a", "ids": []string{"absent"}}}, expect: expected{rows: map[string]map[string]any{}}},
		{desc: "small insertion applies defaults and reader trimming", input: input{body: body("p2", "  small  ")}, expect: expected{rows: map[string]map[string]any{"p1": seed, "p2": {"inlinebody": encoded("small"), "compression": "none", "redacted": float64(0), "sizebytes": float64(9)}}, rawRows: map[string]map[string]any{"p2": {"inlinebody": "  small  ", "compression": "none"}}}},
		{desc: "1024 byte insertion remains uncompressed", input: input{body: body("p2", strings.Repeat("x", 1024))}, expect: expected{rows: map[string]map[string]any{"p1": seed, "p2": {"inlinebody": encoded(strings.Repeat("x", 1024)), "compression": "none", "sizebytes": float64(1024)}}, rawRows: map[string]map[string]any{"p2": {"compression": "none", "sizebytes": float64(1024)}}}},
		{desc: "1025 byte insertion compresses and reader restores binary content", input: input{body: body("p2", strings.Repeat("x", 1025))}, expect: expected{rows: map[string]map[string]any{"p1": seed, "p2": {"inlinebody": encoded(strings.Repeat("x", 1025)), "compression": ""}}, rawRows: map[string]map[string]any{"p2": {"compression": "gzip"}}}},
		{desc: "sparse metadata change preserves binary content and omitted fields", input: input{body: `{"data":[{"id":"p1","digest":null,"subtype":"changed"}]}`}, expect: expected{rows: map[string]map[string]any{"p1": {"inlinebody": encoded("seed body"), "kind": "request", "digest": nil, "subtype": "changed", "sizebytes": float64(9), "redacted": float64(1)}}}},
		{desc: "sparse binary replacement marks derived gzip fields", input: input{body: func() string {
			raw, err := json.Marshal(map[string]any{"data": []map[string]any{{"id": "p1", "inlineBody": wireBytes(strings.Repeat("body", 400))}}})
			must(t, err)
			return string(raw)
		}()}, expect: expected{rows: map[string]map[string]any{"p1": {"kind": "request", "inlinebody": encoded(strings.Repeat("body", 400)), "compression": "", "digest": "seed-digest"}}, rawRows: map[string]map[string]any{"p1": {"compression": "gzip"}}}},
		{desc: "object storage clears inline body and preserves URI", input: input{body: `{"data":[{"id":"p1","storage":"object"}]}`}, expect: expected{rows: map[string]map[string]any{"p1": {"storage": "object", "inlinebody": nil, "uri": "object://seed", "sizebytes": float64(9), "compression": "none"}}}},
		{desc: "explicit null binary value differs from omission", input: input{body: `{"data":[{"id":"p1","inlineBody":null}]}`}, expect: expected{rows: map[string]map[string]any{"p1": {"inlinebody": nil, "storage": "inline", "sizebytes": float64(9), "digest": "seed-digest"}}}},
		{desc: "explicit numeric zero is rejected by the authored required rule", input: input{body: `{"data":[{"id":"p1","sizeBytes":0}]}`}, expect: expected{failed: true, rows: map[string]map[string]any{"p1": seed}}},
		{desc: "missing insertion kind fails before mutation", input: input{body: `{"data":[{"id":"p2","tenantId":"tenant-a","mimeType":"text/plain","sizeBytes":1,"storage":"inline"}]}`}, expect: expected{failed: true, rows: map[string]map[string]any{"p1": seed}}},
		{desc: "late database error rolls back an earlier sparse update", input: input{body: `{"data":[{"id":"p1","digest":"changed"},{"id":"p-reject","tenantId":"tenant-a","kind":"request","mimeType":"text/plain","sizeBytes":1,"storage":"inline"}]}`, filters: map[string]any{"tenantID": "tenant-a", "ids": []string{"p1", "p-reject"}}}, expect: expected{failed: true, rows: map[string]map[string]any{"p1": seed}}},
		{desc: "malformed gzip keeps the legacy fallback and trimming behavior", input: input{filters: map[string]any{"tenantID": "tenant-a", "ids": []string{"p-malformed"}}}, expect: expected{rows: map[string]map[string]any{"p-malformed": {"inlinebody": encoded("broken gzip"), "compression": "gzip"}}, rawRows: map[string]map[string]any{"p-malformed": {"inlinebody": "  broken gzip  ", "compression": "gzip"}}}},
	}
	for _, test := range cases {
		t.Run(test.desc, func(t *testing.T) {
			_, oldPath := payloadFixture(t, project)
			db, _ := payloadFixture(t, project)
			filters := test.input.filters
			if filters == nil {
				filters = defaultFilters
			}
			query := map[string]json.RawMessage{}
			for key, value := range filters {
				raw, err := json.Marshal(value)
				must(t, err)
				query[key] = raw
			}
			probe := payloadProbeRequest{Component: "payload", DBPath: oldPath, Body: test.input.body, Filters: query}
			before := invokePayloadProbe(t, legacy, probe)
			rt, binaryKey, rawKey := payloadRuntime(t, db)
			after := probeResult{Rows: []json.RawMessage{}}
			if test.input.body != "" {
				request := httptest.NewRequest("PATCH", "/v1/api/agently/payload", strings.NewReader(test.input.body))
				request.Header.Set("Content-Type", "application/json")
				scope, err := requestprovider.New(request)
				must(t, err)
				result, runErr := rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/payload", scope)
				must(t, scope.Close())
				after.Failed = runErr != nil
				if runErr != nil {
					after.Error = runErr.Error()
				} else {
					after.Output, _ = json.Marshal(result.(*payloadwrite.Output).Data)
				}
			}
			after.Rows = payloadRead(t, rt, binaryKey, query, false)
			if before.Failed != test.expect.failed || after.Failed != test.expect.failed {
				t.Fatalf("failure parity: legacy=%t (%s), v1=%t (%s), expected=%t", before.Failed, before.Error, after.Failed, after.Error, test.expect.failed)
			}
			oldRows, newRows := normalizeRows(t, before.Rows), normalizeRows(t, after.Rows)
			if !reflect.DeepEqual(oldRows, newRows) {
				t.Fatalf("binary reader parity\nlegacy=%s\nv1=%s", pretty(oldRows), pretty(newRows))
			}
			assertPayloadRows(t, newRows, test.expect.rows, true)
			// Read stored bytes separately through the generated raw presentation,
			// preserving coverage of the write contract independently of OnFetch.
			probe.Body = ""
			probe.Raw = true
			beforeRaw := invokePayloadProbe(t, legacy, probe)
			newRaw := payloadRead(t, rt, rawKey, query, true)
			if !reflect.DeepEqual(normalizeRows(t, beforeRaw.Rows), normalizeRows(t, newRaw)) {
				t.Fatalf("stored row parity\nlegacy=%s\nv1=%s", pretty(normalizeRows(t, beforeRaw.Rows)), pretty(normalizeRows(t, newRaw)))
			}
			assertPayloadRows(t, normalizeRows(t, newRaw), test.expect.rawRows, false)
			if test.input.body != "" && !test.expect.failed {
				var oldOutput, newOutput []json.RawMessage
				must(t, json.Unmarshal(before.Output, &oldOutput))
				must(t, json.Unmarshal(after.Output, &newOutput))
				if !reflect.DeepEqual(normalizeRowsInOrder(t, oldOutput), normalizeRowsInOrder(t, newOutput)) {
					t.Fatalf("transformed PATCH output parity\nlegacy=%s\nv1=%s", before.Output, after.Output)
				}
			}
		})
	}
}

func invokePayloadProbe(t *testing.T, binary string, request payloadProbeRequest) probeResult {
	t.Helper()
	payload, err := json.Marshal(request)
	must(t, err)
	command := exec.Command(binary)
	command.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("legacy payload: %v\n%s", err, stderr.String())
	}
	var result probeResult
	must(t, json.Unmarshal(bytes.TrimSpace(output), &result))
	return result
}
func assertPayloadRows(t *testing.T, rows []map[string]any, want map[string]map[string]any, exact bool) {
	t.Helper()
	if exact && len(rows) != len(want) {
		t.Fatalf("rows=%d, expected=%d", len(rows), len(want))
	}
	index := map[string]map[string]any{}
	for _, row := range rows {
		index[row["id"].(string)] = row
	}
	for id, fields := range want {
		row, ok := index[id]
		if !ok {
			t.Fatalf("missing row %s", id)
		}
		for field, value := range fields {
			if !reflect.DeepEqual(row[field], value) {
				t.Errorf("%s.%s=%v, expected=%v", id, field, row[field], value)
			}
		}
	}
}
func payloadFixture(t *testing.T, project string) (*sql.DB, string) {
	t.Helper()
	db, path := goalFixture(t, project)
	_, err := db.Exec(`INSERT INTO call_payload(id,tenant_id,kind,mime_type,size_bytes,digest,storage,inline_body,uri,compression,redacted,created_at)
 VALUES ('p1','tenant-a','request','text/plain',9,'seed-digest','inline','seed body','object://seed','none',1,'2026-01-01 00:00:00'),
 ('p-foreign','tenant-b','response','text/plain',7,NULL,'inline','foreign',NULL,'none',0,'2026-01-01 00:00:00'),
 ('p-malformed','tenant-a','request','text/plain',15,NULL,'inline','  broken gzip  ',NULL,'gzip',0,'2026-01-01 00:00:00');
 CREATE TRIGGER reject_payload BEFORE INSERT ON call_payload WHEN NEW.id='p-reject' BEGIN SELECT RAISE(ABORT,'fixture payload rejection'); END;`)
	must(t, err)
	return db, path
}
func payloadRead(t *testing.T, rt *druntime.Runtime, key spec.Key, filters map[string]json.RawMessage, raw bool) []json.RawMessage {
	t.Helper()
	binary := &binaryread.Input{}
	if value, ok := filters["tenantID"]; ok {
		var v string
		must(t, json.Unmarshal(value, &v))
		binary.SetTenantID(v)
	}
	if value, ok := filters["ids"]; ok {
		var v []string
		must(t, json.Unmarshal(value, &v))
		binary.SetIds(v)
	}
	if value, ok := filters["kind"]; ok {
		var v string
		must(t, json.Unmarshal(value, &v))
		binary.SetKind(v)
	}
	if value, ok := filters["storage"]; ok {
		var v string
		must(t, json.Unmarshal(value, &v))
		binary.SetStorage(v)
	}
	var input any = binary
	path := "/v2/api/agently/payload"
	if raw {
		path = "/v1/api/agently/payload"
		value := &rawread.PayloadRowsInput{}
		if binary.Has != nil {
			if binary.Has.TenantID {
				value.SetTenantID(binary.TenantID)
			}
			if binary.Has.Ids {
				value.SetIds(binary.Ids)
			}
			if binary.Has.Kind {
				value.SetKind(binary.Kind)
			}
			if binary.Has.Storage {
				value.SetStorage(binary.Storage)
			}
		}
		input = value
	}
	result, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: path}}, Input: input})
	must(t, err)
	rows := []json.RawMessage{}
	if raw {
		for _, row := range result.(*rawread.PayloadRowsOutput).Data {
			data, err := json.Marshal(row)
			must(t, err)
			rows = append(rows, data)
		}
	} else {
		for _, row := range result.(*binaryread.Output).Data {
			data, err := json.Marshal(row)
			must(t, err)
			rows = append(rows, data)
		}
	}
	return rows
}
func payloadRuntime(t *testing.T, db *sql.DB) (*druntime.Runtime, spec.Key, spec.Key) {
	t.Helper()
	resources := resource.New()
	must(t, resources.Register(binaryread.ReaderDatlyResourceNamespace, binaryread.ReaderDatlyResources))
	must(t, resources.Register(rawread.ReaderDatlyResourceNamespace, rawread.ReaderDatlyResources))
	must(t, resources.Register(payloadwrite.WriterDatlyResourceNamespace, payloadwrite.WriterDatlyResources))
	binary := payloadArtifact(t, resources, reflect.TypeFor[binaryread.ReaderComponent](), reflect.TypeFor[binaryread.Input](), reflect.TypeFor[binaryread.Output]())
	raw := payloadArtifact(t, resources, reflect.TypeFor[rawread.ReaderComponent](), reflect.TypeFor[rawread.PayloadRowsInput](), reflect.TypeFor[rawread.PayloadRowsOutput]())
	mutation := payloadArtifact(t, resources, reflect.TypeFor[payloadwrite.WriterComponent](), reflect.TypeFor[payloadwrite.Input](), reflect.TypeFor[payloadwrite.Output]())
	binaryReader, err := binary.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	rawReader, err := raw.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	views, err := viewprovider.New(viewprovider.Config{Dependencies: mutation.ViewDependencies, Input: mutation.Input, SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	handler, err := writer.New(mutation.Component, reflect.TypeFor[payloadwrite.Input](), reflect.TypeFor[payloadwrite.Output](), "patch")
	must(t, err)
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{
		{Component: binary.Component, Input: binary.Input, Output: binary.Output, OutputType: reflect.TypeFor[binaryread.Output](), Reader: binaryReader},
		{Component: raw.Component, Input: raw.Input, Output: raw.Output, OutputType: reflect.TypeFor[rawread.PayloadRowsOutput](), Reader: rawReader},
		{Component: mutation.Component, Input: mutation.Input, Output: mutation.Output, OutputType: reflect.TypeFor[payloadwrite.Output](), Handler: handler, Providers: []locator.Provider{views}, DataSource: dml.Source{DB: db}},
	}, druntime.WithResources(resources))
	must(t, err)
	return rt, binary.Component.Key, raw.Component.Key
}
func payloadArtifact(t *testing.T, resources *resource.Store, holder, input, output reflect.Type) *bootstrap.Artifact {
	t.Helper()
	field, _ := holder.FieldByName("Contract")
	tag, present, err := dtag.ParseComponent(field.Tag)
	must(t, err)
	if !present {
		t.Fatal("missing transcribed holder")
	}
	source := &bootstrap.RouteSource{HolderType: holder.Name(), FieldName: field.Name, PackageName: filepath.Base(holder.PkgPath()), PackagePath: holder.PkgPath(), Tag: tag, InputType: input.Name(), OutputType: output.Name()}
	component, err := source.Resolve(input, output)
	must(t, err)
	component.Routes[0].Internal = false // Test-only HTTP-binding fixture.
	artifact, err := bootstrap.BuildArtifact(bootstrap.ArtifactInput{Component: component, InputType: input, OutputType: output, Resources: resources})
	must(t, err)
	return artifact
}
