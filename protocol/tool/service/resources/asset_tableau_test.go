package resources

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/agently-core/protocol/tool"
	scratchpadsvc "github.com/viant/agently-core/protocol/tool/service/scratchpad"
)

const tableauFixture = `<?xml version="1.0" encoding="utf-8"?>
<workbook name="Synthetic workbook" version="18.1" source-build="2026.1">
  <datasources>
    <datasource name="sales" caption="Sample sales">
      <connection class="federated">
        <named-connections><named-connection name="sample">
          <connection class="postgres" dbname="sample" schema="public" server="private.invalid"
                      username="unlisted-account" password="discarded-fixture-value" token="hidden-fixture-value"/>
        </named-connection></named-connections>
        <relation type="join" join="left">
          <clause type="join"><expression op="="><expression op="[orders].[id]"/><expression op="[labels].[id]"/></expression></clause>
          <relation type="table" name="orders" table="[orders]"/>
          <relation type="text" name="labels" connection="sample">SELECT id, label FROM labels WHERE id &gt; 0</relation>
          <relation type="text" name="unsafe">SELECT 'token=synthetic-auth-marker'</relation>
        </relation>
      </connection>
      <column name="[Amount]" datatype="real" role="measure" aggregation="Sum"/>
      <column name="[Computed]" datatype="real" role="measure">
        <calculation class="tableau" formula="SUM([Amount]) / [Count] + LEN('[quoted]')"/>
      </column>
      <filter class="categorical" column="[Region]" context="true">
        <groupfilter function="member" member="&quot;West&quot;"/>
      </filter>
    </datasource>
    <datasource name="Parameters">
      <column name="[Limit]" caption="Limit" datatype="integer" param-domain-type="range" value="4">
        <range min="1" max="5" granularity="1"/>
      </column>
      <column name="[Pick]" datatype="string" param-domain-type="list" value="&quot;East&quot;">
        <members><member value="&quot;East&quot;" alias="E"/><member value="&quot;West&quot;" alias="W"/></members>
      </column>
    </datasource>
  </datasources>
  <worksheets><worksheet name="Trend"><table>
    <view>
      <datasources><datasource name="sales"/></datasources>
      <datasource-dependencies datasource="sales">
        <column-instance name="[Sum:Amount]" column="[Amount]" derivation="Sum">
          <table-calc type="PctTotal" ordering-type="Rows"><partition field="[Region]"/></table-calc>
        </column-instance>
      </datasource-dependencies>
      <filter class="quantitative" column="[Amount]"><min>0</min><max>99</max></filter>
    </view>
    <panes><pane id="1"><mark class="Line"/><encodings><color column="[Region]"/></encodings></pane></panes>
    <rows>[Computed]</rows><cols>[Date]</cols>
    <unsupported-markup><column name="UNMODELED_CONTENT"/></unsupported-markup>
  </table></worksheet></worksheets>
  <dashboards><dashboard name="Overview"><size sizing-mode="fixed"/><zones>
    <zone id="1" type-v2="layout-flow" w="100000"><zone id="2" name="Trend" x="0" w="50000"/></zone>
  </zones><actions><action name="Select region"><source worksheet="Trend"/><target dashboard="Overview"/></action></actions></dashboard></dashboards>
  <windows><window class="dashboard" name="Overview"/></windows>
</workbook>`

func tableauPackage(t *testing.T, name, definition string, extra ...*zip.FileHeader) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	if name != "" {
		w, err := writer.Create(name)
		require.NoError(t, err)
		_, err = io.WriteString(w, definition)
		require.NoError(t, err)
	}
	for _, header := range extra {
		_, err := writer.CreateRaw(header)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return buffer.Bytes()
}

func TestTableauResourceInspectionThroughToolRegistry(t *testing.T) {
	ctx := assetContext(t)
	packaged := tableauPackage(t, "nested/synthetic.twb", tableauFixture,
		&zip.FileHeader{Name: "Data/embedded.csv", Method: 99},
		&zip.FileHeader{Name: "Data/embedded.hyper", Method: 99},
		&zip.FileHeader{Name: "Data/embedded.tde", Method: 99},
	)
	uri := publishFixture(t, ctx, "synthetic.twbx", "application/octet-stream", packaged)
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	reg, err := tool.NewDefaultRegistry(mgr)
	require.NoError(t, err)
	require.NoError(t, tool.AddInternalService(reg, New(nil)))
	raw, err := reg.Execute(ctx, "resources:inspect", map[string]any{"uri": uri})
	require.NoError(t, err)
	for _, redacted := range []string{"unlisted-account", "discarded-fixture-value", "hidden-fixture-value", "synthetic-auth-marker", "private.invalid", "UNMODELED_CONTENT", "embedded.csv", "embedded.hyper", "<workbook"} {
		require.NotContains(t, raw, redacted)
	}
	var result InspectOutput
	require.NoError(t, json.Unmarshal([]byte(raw), &result))
	require.Equal(t, "tableau", result.Kind)
	require.Equal(t, map[string]string{
		"definition": "synthetic.twb", "name": "Synthetic workbook", "version": "18.1",
		"source-build": "2026.1", "hasExtract": "true", "unmodeledElements": "1",
	}, result.Metadata)
	require.Equal(t, []string{"metadata", "native"}, result.Capabilities["read"])
	require.True(t, result.Complete)
	categories := map[string][]ResourceComponent{}
	for _, c := range result.Components {
		categories[c.Kind] = append(categories[c.Kind], c)
	}
	require.Equal(t, map[string]string{"class": "postgres", "dbname": "sample", "schema": "public", "unmodeledAttributes": "4"}, categories["connection"][2].Metadata)
	require.Equal(t, "left", categories["relation"][0].Metadata["join"])
	require.Equal(t, "[orders].[id]", categories["expression"][1].Metadata["op"])
	require.Equal(t, "[orders]", categories["relation"][1].Metadata["table"])
	require.Equal(t, "SELECT id, label FROM labels WHERE id > 0", categories["relation"][2].Metadata["sql"])
	require.Equal(t, "credential-like value", categories["relation"][3].Metadata["omitted-sql"])
	require.Equal(t, "Sum", categories["field"][0].Metadata["aggregation"])
	require.Equal(t, "SUM([Amount]) / [Count] + LEN('[quoted]')", categories["calculation"][0].Metadata["formula"])
	require.Equal(t, "[Amount], [Count]", categories["calculation"][0].Metadata["references"])
	require.Equal(t, "4", categories["parameter"][0].Metadata["value"])
	require.Equal(t, "5", categories["parameterRange"][0].Metadata["max"])
	require.Equal(t, "\"East\"", categories["parameterMember"][0].Metadata["value"])
	require.Equal(t, "W", categories["parameterMember"][1].Metadata["alias"])
	require.Equal(t, "datasource", categories["filter"][0].Metadata["scope"])
	require.Equal(t, "true", categories["filter"][0].Metadata["context"])
	require.Equal(t, "worksheet", categories["filter"][1].Metadata["scope"])
	require.Equal(t, "99", categories["filterBound"][1].Metadata["value"])
	require.Equal(t, "Line", categories["mark"][0].Metadata["class"])
	require.Equal(t, "color", categories["encoding"][0].Name)
	require.Equal(t, "[Region]", categories["encoding"][0].Metadata["column"])
	require.Equal(t, "[Computed]", categories["shelf"][0].Metadata["expression"])
	require.Equal(t, "Sum", categories["fieldInstance"][0].Metadata["derivation"])
	require.Equal(t, "Rows", categories["tableCalculation"][0].Metadata["ordering-type"])
	require.Equal(t, "[Region]", categories["partition"][0].Metadata["field"])
	require.Equal(t, categories["zone"][0].ID, categories["zone"][1].ParentID)
	require.Equal(t, "50000", categories["zone"][1].Metadata["w"])
	require.Equal(t, "Trend", categories["actionSource"][0].Metadata["worksheet"])
	require.Equal(t, "Overview", categories["actionTarget"][0].Metadata["dashboard"])
	require.Equal(t, "Overview", categories["tab"][0].Name)

	selected := categories["dashboard"][0].ID
	inspect := map[string]any{"uri": uri, "select": map[string]any{"componentId": selected}, "limit": 2, "expectedVersion": result.Version}
	var componentIDs []string
	for {
		raw, err = reg.Execute(ctx, "resources:inspect", inspect)
		require.NoError(t, err)
		page := InspectOutput{}
		require.NoError(t, json.Unmarshal([]byte(raw), &page))
		require.NotEmpty(t, page.Components)
		for _, component := range page.Components {
			componentIDs = append(componentIDs, component.ID)
		}
		if page.Complete {
			break
		}
		inspect["cursor"] = page.NextCursor
	}
	require.Equal(t, []string{selected, categories["layoutSize"][0].ID, categories["zone"][0].ID, categories["zone"][1].ID,
		categories["action"][0].ID, categories["actionSource"][0].ID, categories["actionTarget"][0].ID}, componentIDs)

	read := map[string]any{"uri": uri, "representation": "metadata", "select": inspect["select"], "limits": map[string]any{"maxRows": 1}}
	raw, err = reg.Execute(ctx, "resources:read", read)
	require.NoError(t, err)
	var data ReadOutput
	require.NoError(t, json.Unmarshal([]byte(raw), &data))
	require.Len(t, data.Components, 1)
	require.Equal(t, selected, data.Components[0].ID)
	require.NotEmpty(t, data.Cursor)
	read["cursor"] = data.Cursor
	raw, err = reg.Execute(ctx, "resources:read", read)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(raw), &data))
	require.Equal(t, componentIDs[1], data.Components[0].ID)
	read["select"] = map[string]any{"componentId": categories["worksheet"][0].ID}
	_, err = reg.Execute(ctx, "resources:read", read)
	require.Error(t, err)
	other := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "other-user"})
	_, err = reg.Execute(other, "resources:inspect", map[string]any{"uri": uri})
	require.Error(t, err)

	// The default metadata representation supports the cursors it returns.
	defaultRead := map[string]any{"uri": uri, "limits": map[string]any{"maxRows": 1}}
	var defaultIDs []string
	for {
		raw, err = reg.Execute(ctx, "resources:read", defaultRead)
		require.NoError(t, err)
		page := ReadOutput{}
		require.NoError(t, json.Unmarshal([]byte(raw), &page))
		require.Len(t, page.Components, 1)
		defaultIDs = append(defaultIDs, page.Components[0].ID)
		if page.Cursor == "" {
			require.False(t, page.Coverage.Truncated)
			break
		}
		defaultRead["cursor"] = page.Cursor
	}
	var inspectedIDs []string
	for _, component := range result.Components {
		inspectedIDs = append(inspectedIDs, component.ID)
	}
	require.Equal(t, inspectedIDs, defaultIDs)
	raw, err = reg.Execute(ctx, "resources:read", map[string]any{"uri": uri, "select": inspect["select"]})
	require.NoError(t, err)
	defaultSelected := ReadOutput{}
	require.NoError(t, json.Unmarshal([]byte(raw), &defaultSelected))
	require.Equal(t, selected, defaultSelected.Components[0].ID)
	require.Len(t, defaultSelected.Components, len(componentIDs))
	csvURI := publishFixture(t, ctx, "sample.csv", "text/csv", []byte("name\nExample\n"))
	_, err = reg.Execute(ctx, "resources:read", map[string]any{"uri": csvURI, "select": map[string]any{"range": "A1:A2"}})
	require.ErrorContains(t, err, "specify a representation")
}

func TestTableauDatasourceReferencesAndFilterScopes(t *testing.T) {
	ctx := assetContext(t)
	workbook := `<workbook>
  <datasources>
    <datasource name="inventory"><filter class="categorical" column="[Warehouse]"/></datasource>
    <datasource name="rates"/>
  </datasources>
  <shared-views><shared-view name="Shared inventory">
    <datasources><datasource name="inventory"/></datasources>
    <filter class="categorical" column="[In Stock]"><groupfilter function="member" member="true"/></filter>
  </shared-view></shared-views>
  <worksheets><worksheet name="Inventory by site"><table><view>
    <datasources><datasource name="inventory"/></datasources>
    <filter class="categorical" column="[Product]"/>
  </view></table></worksheet></worksheets>
  <dashboards><dashboard name="Overview">
    <datasources><datasource name="rates"/></datasources>
  </dashboard></dashboards>
  <actions><datasources><datasource name="inventory"/></datasources></actions>
</workbook>`
	uri := publishFixture(t, ctx, "shared-view.twb", "application/xml", []byte(workbook))
	var inspected InspectOutput
	require.NoError(t, New(nil).inspect(ctx, &InspectInput{URI: uri}, &inspected))
	require.True(t, inspected.Complete)
	byKind := map[string][]ResourceComponent{}
	for _, component := range inspected.Components {
		byKind[component.Kind] = append(byKind[component.Kind], component)
	}
	require.Len(t, byKind["sharedView"], 1)
	require.Len(t, byKind["worksheet"], 1)
	require.Len(t, byKind["dashboard"], 1)
	shared := byKind["sharedView"][0].ID
	worksheet := byKind["worksheet"][0].ID
	dashboard := byKind["dashboard"][0].ID

	t.Run("authored datasource identities", func(t *testing.T) {
		sources := byKind["datasource"]
		require.Len(t, sources, 2)
		require.Equal(t, "inventory", sources[0].Metadata["name"])
		require.Equal(t, "rates", sources[1].Metadata["name"])
		for _, source := range sources {
			require.Empty(t, source.ParentID)
		}
		references := byKind["datasourceReference"]
		require.Len(t, references, 4)
		for i, want := range []struct{ name, parent string }{
			{"inventory", shared}, {"inventory", worksheet}, {"rates", dashboard}, {"inventory", ""},
		} {
			require.Equal(t, want.name, references[i].Metadata["name"])
			require.Equal(t, want.parent, references[i].ParentID)
		}
	})
	t.Run("distinct filter scopes", func(t *testing.T) {
		filters := byKind["filter"]
		require.Len(t, filters, 3)
		require.Equal(t, "datasource", filters[0].Metadata["scope"])
		require.Equal(t, "sharedView", filters[1].Metadata["scope"])
		require.Equal(t, "[In Stock]", filters[1].Metadata["column"])
		require.Equal(t, shared, filters[1].ParentID)
		require.Equal(t, "worksheet", filters[2].Metadata["scope"])
		require.Equal(t, worksheet, filters[2].ParentID)
	})
}

func TestTableauLongSQLPagedWithoutLosingText(t *testing.T) {
	ctx := assetContext(t)
	sql := "SELECT '" + strings.Repeat("雪", 5000) + "' AS name"
	workbook := "<workbook><datasources><datasource name='Example'><connection class='sql'><relation name='Query' type='text'>" +
		sql + "</relation></connection></datasource></datasources></workbook>"
	name := filepath.Join(t.TempDir(), "long.TWB")
	require.NoError(t, os.WriteFile(name, []byte(workbook), 0600))
	s := New(nil)
	var metadata InspectOutput
	require.NoError(t, s.inspect(ctx, &InspectInput{URI: name}, &metadata))
	require.Equal(t, "false", metadata.Metadata["hasExtract"])
	relation := metadata.Components[2]
	require.Equal(t, "4", relation.Metadata["sql-chunks"])
	input := &ReadInput{URI: name, Representation: "metadata", ExpectedVersion: metadata.Version,
		Select: &ResourceSelection{ComponentID: relation.ID}, Limits: &ResourceLimits{MaxOutputBytes: 8192}}
	var output ReadOutput
	var content strings.Builder
	part := 1
	for {
		require.NoError(t, s.read(ctx, input, &output))
		for _, component := range output.Components {
			if component.Kind == "metadataText" {
				require.Equal(t, relation.ID, component.ParentID)
				require.Equal(t, "sql", component.Name)
				require.Equal(t, strconv.Itoa(part), component.Metadata["part"])
				content.WriteString(component.Metadata["text"])
				part++
			}
		}
		if !output.Coverage.Truncated {
			break
		}
		input.Cursor = output.Cursor
	}
	require.Empty(t, output.Cursor)
	require.Equal(t, sql, content.String())

	// An ordinary read uses metadata by default for both workspace files and uploads.
	require.NoError(t, s.read(ctx, &ReadInput{URI: name}, &output))
	require.Empty(t, output.Content)
	uri := publishFixture(t, ctx, "long.twb", "application/xml", []byte(workbook))
	require.NoError(t, s.read(ctx, &ReadInput{URI: uri}, &output))
	require.Equal(t, "long.twb", output.Metadata["definition"])
	var textPreview ReadOutput
	require.NoError(t, s.read(ctx, &ReadInput{URI: uri, Representation: "text", Limits: &ResourceLimits{MaxOutputBytes: 16}}, &textPreview))
	require.Equal(t, workbook[:16], textPreview.Content)
	var native ReadOutput
	require.NoError(t, s.read(ctx, &ReadInput{URI: uri, Representation: "native"}, &native))
	original, err := scratchpadsvc.New().ReadArtifactPayload(ctx, native.Native.URI)
	require.NoError(t, err)
	require.Equal(t, workbook, string(original))
	require.Error(t, s.read(ctx, &ReadInput{URI: uri, Representation: "table"}, &ReadOutput{}))
	require.NoError(t, os.WriteFile(name, []byte("<workbook/>"), 0600))
	require.ErrorContains(t, s.read(ctx, input, &output), "resource_changed")
}

func TestTableauLongFormulaPagedThroughToolRegistry(t *testing.T) {
	ctx := assetContext(t)
	formula := "SUM([Amount]) + LEN(\"" + strings.Repeat("雪", 5000) + "\") + [Count]"
	var encodedFormula bytes.Buffer
	require.NoError(t, xml.EscapeText(&encodedFormula, []byte(formula)))
	workbook := "<workbook><datasources><datasource name='Example'><column name='[Computed]' datatype='real'>" +
		"<calculation class='tableau' formula=\"" + encodedFormula.String() + "\"/>" +
		"</column></datasource></datasources></workbook>"
	uri := publishFixture(t, ctx, "long-formula.twb", "application/xml", []byte(workbook))
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	reg, err := tool.NewDefaultRegistry(mgr)
	require.NoError(t, err)
	require.NoError(t, tool.AddInternalService(reg, New(nil)))

	raw, err := reg.Execute(ctx, "resources:inspect", map[string]any{"uri": uri})
	require.NoError(t, err)
	var inspected InspectOutput
	require.NoError(t, json.Unmarshal([]byte(raw), &inspected))
	require.True(t, inspected.Complete)
	var calculation ResourceComponent
	for _, component := range inspected.Components {
		if component.Kind == "calculation" {
			calculation = component
		}
	}
	require.NotEmpty(t, calculation.ID)
	require.Equal(t, "4", calculation.Metadata["formula-chunks"])
	require.Equal(t, "[Amount], [Count]", calculation.Metadata["references"])
	require.Empty(t, calculation.Metadata["formula"])

	input := map[string]any{
		"uri": uri, "representation": "metadata", "expectedVersion": inspected.Version,
		"select": map[string]any{"componentId": calculation.ID},
		"limits": map[string]any{"maxRows": 1},
	}
	var reconstructed strings.Builder
	parts := 0
	seenIDs := map[string]bool{}
	for {
		raw, err = reg.Execute(ctx, "resources:read", input)
		require.NoError(t, err)
		var page ReadOutput
		require.NoError(t, json.Unmarshal([]byte(raw), &page))
		require.Len(t, page.Components, 1)
		component := page.Components[0]
		require.False(t, seenIDs[component.ID])
		seenIDs[component.ID] = true
		if component.Kind == "metadataText" {
			parts++
			require.Equal(t, calculation.ID, component.ParentID)
			require.Equal(t, "formula", component.Name)
			require.Equal(t, strconv.Itoa(parts), component.Metadata["part"])
			reconstructed.WriteString(component.Metadata["text"])
		}
		if page.Cursor == "" {
			require.False(t, page.Coverage.Truncated)
			break
		}
		input["cursor"] = page.Cursor
	}
	require.Equal(t, 4, parts)
	require.Equal(t, formula, reconstructed.String())
}

func TestTableauInvalidDefinitionAndArchive(t *testing.T) {
	ctx := assetContext(t)
	s := New(nil)
	for _, invalid := range []string{"<rows/>", "<workbook/><workbook/>", "<!DOCTYPE workbook><workbook/>", "<workbook>", "text<workbook/>",
		"<workbook>" + strings.Repeat("<element>", maxTableauDepth) + strings.Repeat("</element>", maxTableauDepth) + "</workbook>"} {
		uri := publishFixture(t, ctx, "invalid.twb", "application/xml", []byte(invalid))
		require.Error(t, s.inspect(ctx, &InspectInput{URI: uri}, &InspectOutput{}))
	}
	for _, tc := range []struct {
		name, message string
		extra         []*zip.FileHeader
	}{
		{"nested/safe.twb", "entry", []*zip.FileHeader{{Name: "../escape.csv", Method: zip.Store}}},
		{"nested/safe.twb", "entry", []*zip.FileHeader{{Name: "nested/SAFE.TWB", Method: zip.Store}}},
		{"nested/safe.twb", "multiple", []*zip.FileHeader{{Name: "other.twb", Method: zip.Store}}},
		{"", "no workbook", []*zip.FileHeader{{Name: "file.csv", Method: zip.Store}}},
		{"", "inspection limit", []*zip.FileHeader{{Name: "oversize.twb", Method: zip.Deflate, UncompressedSize64: maxTableauDefinitionBytes + 1}}},
	} {
		uri := publishFixture(t, ctx, "invalid.twbx", "application/zip", tableauPackage(t, tc.name, "<workbook/>", tc.extra...))
		require.ErrorContains(t, s.inspect(ctx, &InspectInput{URI: uri}, &InspectOutput{}), tc.message)
	}
	canceled, cancel := context.WithCancel(ctx)
	defer cancel()
	_, err := parseTableauMetadata(canceled, &tableauCancelReader{cancel: cancel, source: strings.NewReader(tableauFixture)}, "canceled.twb", false)
	require.ErrorIs(t, err, context.Canceled)
}

func TestTableauLargeVerifiedArchiveKeepsDefinitionBounded(t *testing.T) {
	ctx := assetContext(t)
	spool, err := os.CreateTemp(t.TempDir(), "tableau-archive-*")
	require.NoError(t, err)
	defer spool.Close()
	writer := zip.NewWriter(spool)
	embedded, err := writer.CreateHeader(&zip.FileHeader{Name: "Data/embedded.csv", Method: zip.Store})
	require.NoError(t, err)
	_, err = io.Copy(embedded, io.LimitReader(&repeatedArtifactReader{pattern: []byte("unread-fact-record\n")}, 65<<20))
	require.NoError(t, err)
	definition, err := writer.Create("workbook.twb")
	require.NoError(t, err)
	_, err = io.WriteString(definition, tableauFixture)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	_, err = spool.Seek(0, io.SeekStart)
	require.NoError(t, err)
	descriptor, err := scratchpadsvc.New().PublishArtifactStream(ctx, "large.twbx", "application/zip", "", spool)
	require.NoError(t, err)
	require.Greater(t, descriptor.SizeBytes, scratchpadsvc.MaxArtifactBytes)
	s := New(nil)
	loaded, err := s.loadAsset(ctx, &ReadInput{URI: descriptor.URI}, descriptor.SHA256)
	require.NoError(t, err)
	defer loaded.close()
	require.LessOrEqual(t, len(loaded.data), 512)
	metadata, err := loaded.tableauMetadata(ctx)
	require.NoError(t, err)
	require.Equal(t, "18.1", metadata.properties["version"])
	encoded, err := json.Marshal(metadata.components)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "unread-fact-record")
	require.Less(t, len(encoded), maxReadOutputBytes)
}

func TestTableauFormulaReferencesAndCredentialOmission(t *testing.T) {
	require.Equal(t, "[source].[field], [Total]", tableauReferences(
		`[source].[field] + [Total] // [comment]
		+ LEN("[literal]") /* [another comment] */ + [source].[field]`))
	for _, text := range []string{
		"password=synthetic-value", "access_token=synthetic-value",
		`{"refresh_token": "synthetic-value"}`, "api_key%3Dsynthetic-value",
		"SELECT 'x' IDENTIFIED BY 'synthetic-value'",
	} {
		properties := map[string]string{}
		tableauSet(properties, "value", text)
		require.Empty(t, properties["value"])
		require.Equal(t, "credential-like value", properties["omitted-value"])
	}
	reader := &tableauArchiveReader{ctx: context.Background(), source: bytes.NewReader([]byte("example")), bytesLeft: 1, indexOnly: true}
	_, err := reader.ReadAt(make([]byte, 2), 0)
	require.ErrorIs(t, err, errTableauDirectoryLimit)
}

type tableauCancelReader struct {
	cancel context.CancelFunc
	source io.Reader
}

func (r *tableauCancelReader) Read(data []byte) (int, error) {
	n, err := r.source.Read(data)
	r.cancel()
	return n, err
}
