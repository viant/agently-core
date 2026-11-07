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

func TestTableauGroupSortAliasAndFormattedText(t *testing.T) {
	ctx := assetContext(t)
	workbook := `<workbook>
  <datasources><datasource name="inventory">
    <aliases enabled="no"/>
    <column name="[Grade]" caption="Quality grade">
      <aliases enabled="yes"><alias key="&quot;A&quot;" value="First grade"/></aliases>
    </column>
    <group name="[Priority]" caption="Priority stock" auto-column="no" name-style="unqualified" ui-vit-column="[Grade]">
      <groupfilter function="union">
        <groupfilter function="member" level="[Grade]" member="&quot;A&quot;"/>
        <groupfilter function="member" level="[Grade]" member="&quot;B&quot;"/>
      </groupfilter>
    </group>
    <default-sorts><manual-sort column="[Grade]" direction="DESC"><dictionary>
      <bucket>"A"</bucket><bucket>"B"</bucket>
    </dictionary></manual-sort></default-sorts>
  </datasource></datasources>
  <worksheets><worksheet name="Stock">
    <layout-options><title><formatted-text><run>Stock by grade</run></formatted-text></title></layout-options>
    <table>
    <view><manual-sort column="[Grade]" direction="ASC"><dictionary>
      <bucket>"B"</bucket><bucket>"A"</bucket>
    </dictionary></manual-sort></view>
    <panes><pane id="1"><customized-tooltip enabled="true" show-buttons="false">
      <formatted-text>
        <run bold="true" fontsize="12" fontcolor="#123456">Quality: </run>
        <run italic="true">&#10;  &lt;[inventory].[Grade]&gt;</run>
        <script>unmodeled-script-value</script>
      </formatted-text>
    </customized-tooltip>
    <customized-label><formatted-text><run>&lt;[Grade]&gt;</run></formatted-text></customized-label>
    </pane></panes>
  </table></worksheet></worksheets>
  <dashboards><dashboard name="Overview"><zones><zone id="1">
    <formatted-text><run italic="true">Grade summary</run></formatted-text>
  </zone></zones></dashboard></dashboards>
</workbook>`
	uri := publishFixture(t, ctx, "metadata-coverage.twb", "application/xml", []byte(workbook))
	var inspected InspectOutput
	require.NoError(t, New(nil).inspect(ctx, &InspectInput{URI: uri}, &inspected))
	require.True(t, inspected.Complete)
	byKind := map[string][]ResourceComponent{}
	children := map[string][]ResourceComponent{}
	for _, component := range inspected.Components {
		byKind[component.Kind] = append(byKind[component.Kind], component)
		children[component.ParentID] = append(children[component.ParentID], component)
	}

	t.Run("datasource group membership", func(t *testing.T) {
		groups := byKind["datasourceGroup"]
		require.Len(t, groups, 1)
		require.Equal(t, byKind["datasource"][0].ID, groups[0].ParentID)
		require.Equal(t, "[Priority]", groups[0].Metadata["name"])
		require.Equal(t, "Priority stock", groups[0].Metadata["caption"])
		require.Equal(t, "no", groups[0].Metadata["auto-column"])
		require.Equal(t, "unqualified", groups[0].Metadata["name-style"])
		require.Equal(t, "[Grade]", groups[0].Metadata["ui-vit-column"])
		members := byKind["filterGroup"]
		require.Len(t, members, 3)
		require.Equal(t, groups[0].ID, members[0].ParentID)
		require.Equal(t, "union", members[0].Metadata["function"])
		for i, value := range []string{`"A"`, `"B"`} {
			require.Equal(t, members[0].ID, members[i+1].ParentID)
			require.Equal(t, "member", members[i+1].Metadata["function"])
			require.Equal(t, "[Grade]", members[i+1].Metadata["level"])
			require.Equal(t, value, members[i+1].Metadata["member"])
		}
	})
	t.Run("manual sort ordering", func(t *testing.T) {
		sorts := byKind["manualSort"]
		require.Len(t, sorts, 2)
		for i, want := range []struct {
			parent, direction string
			members           []string
		}{
			{byKind["datasource"][0].ID, "DESC", []string{`"A"`, `"B"`}},
			{byKind["worksheet"][0].ID, "ASC", []string{`"B"`, `"A"`}},
		} {
			require.Equal(t, want.parent, sorts[i].ParentID)
			require.Equal(t, "[Grade]", sorts[i].Metadata["column"])
			require.Equal(t, want.direction, sorts[i].Metadata["direction"])
			buckets := children[sorts[i].ID]
			require.Len(t, buckets, len(want.members))
			for j, value := range want.members {
				require.Equal(t, "sortBucket", buckets[j].Kind)
				require.Equal(t, value, buckets[j].Metadata["value"])
			}
		}
	})
	t.Run("field alias", func(t *testing.T) {
		aliases := byKind["aliases"]
		require.Len(t, aliases, 2)
		require.Equal(t, byKind["datasource"][0].ID, aliases[0].ParentID)
		require.Equal(t, "no", aliases[0].Metadata["enabled"])
		require.Equal(t, byKind["field"][0].ID, aliases[1].ParentID)
		require.Equal(t, "yes", aliases[1].Metadata["enabled"])
		alias := byKind["alias"]
		require.Len(t, alias, 1)
		require.Equal(t, aliases[1].ID, alias[0].ParentID)
		require.Equal(t, `"A"`, alias[0].Metadata["key"])
		require.Equal(t, "First grade", alias[0].Metadata["value"])
	})
	t.Run("tooltip runs", func(t *testing.T) {
		tooltips := byKind["tooltip"]
		require.Len(t, tooltips, 1)
		require.Equal(t, byKind["pane"][0].ID, tooltips[0].ParentID)
		require.Equal(t, "true", tooltips[0].Metadata["enabled"])
		require.Equal(t, "false", tooltips[0].Metadata["show-buttons"])
		formatted := children[tooltips[0].ID]
		require.Len(t, formatted, 1)
		require.Equal(t, "formattedText", formatted[0].Kind)
		runs := children[formatted[0].ID]
		require.Len(t, runs, 2)
		for _, run := range runs {
			require.Equal(t, "textRun", run.Kind)
		}
		require.Equal(t, "Quality: ", runs[0].Metadata["text"])
		require.Equal(t, "true", runs[0].Metadata["bold"])
		require.Equal(t, "12", runs[0].Metadata["fontsize"])
		require.Equal(t, "#123456", runs[0].Metadata["fontcolor"])
		require.Equal(t, "\n  <[inventory].[Grade]>", runs[1].Metadata["text"])
		require.Equal(t, "[inventory].[Grade]", runs[1].Metadata["references"])
		require.Equal(t, "true", runs[1].Metadata["italic"])
	})
	t.Run("formatted titles labels and zones", func(t *testing.T) {
		for _, want := range []struct{ owner, text string }{
			{"title", "Stock by grade"}, {"label", "<[Grade]>"}, {"zone", "Grade summary"},
		} {
			require.Len(t, byKind[want.owner], 1)
			formatted := children[byKind[want.owner][0].ID]
			require.Len(t, formatted, 1)
			require.Equal(t, "formattedText", formatted[0].Kind)
			runs := children[formatted[0].ID]
			require.Len(t, runs, 1)
			require.Equal(t, "textRun", runs[0].Kind)
			require.Equal(t, want.text, runs[0].Metadata["text"])
		}
	})
	require.Equal(t, "1", inspected.Metadata["unmodeledElements"])
	encoded, err := json.Marshal(inspected)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "unmodeled-script-value")
}

func TestTableauFormattedTextRedactsAcrossRunBoundaries(t *testing.T) {
	ctx := assetContext(t)
	for _, tc := range []struct {
		name, runs string
	}{
		{"split value", `<run>password=</run><run bold="true">synthetic-marker</run>`},
		{"split keyword", `<run>access_</run><run bold="true">token=synthetic-marker</run>`},
		{"long split value", `<run>password=</run><run bold="true">synthetic-marker` + strings.Repeat("雪", 2000) + `</run>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workbook := "<workbook><worksheets><worksheet><table><panes><pane><customized-tooltip><formatted-text>" +
				tc.runs + "</formatted-text></customized-tooltip></pane></panes></table></worksheet></worksheets></workbook>"
			uri := publishFixture(t, ctx, "redacted-text.twb", "application/xml", []byte(workbook))
			var output InspectOutput
			require.NoError(t, New(nil).inspect(ctx, &InspectInput{URI: uri}, &output))
			require.True(t, output.Complete)
			for _, component := range output.Components {
				require.NotEqual(t, "metadataText", component.Kind)
				if component.Kind == "formattedText" || component.Kind == "textRun" {
					require.Equal(t, "credential-like value", component.Metadata["omitted-text"])
					require.Empty(t, component.Metadata["text"])
					require.Empty(t, component.Metadata["references"])
				}
				if component.Kind == "textRun" && component.Metadata["bold"] != "" {
					require.Equal(t, "true", component.Metadata["bold"])
				}
			}
			encoded, err := json.Marshal(output)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "synthetic-marker")
		})
	}
}

func TestTableauLongFormattedRunPreservesTextThroughPaging(t *testing.T) {
	ctx := assetContext(t)
	text := "  " + strings.Repeat("雪", 2000) + "\n<[Grade]> "
	var escaped bytes.Buffer
	require.NoError(t, xml.EscapeText(&escaped, []byte(text)))
	workbook := "<workbook><worksheets><worksheet><table><panes><pane><customized-tooltip><formatted-text><run bold='true'>" +
		escaped.String() + "</run></formatted-text></customized-tooltip></pane></panes></table></worksheet></worksheets></workbook>"
	uri := publishFixture(t, ctx, "long-tooltip.twb", "application/xml", []byte(workbook))
	service := New(nil)
	var inspected InspectOutput
	require.NoError(t, service.inspect(ctx, &InspectInput{URI: uri}, &inspected))
	var run ResourceComponent
	for _, component := range inspected.Components {
		if component.Kind == "textRun" {
			run = component
		}
	}
	require.NotEmpty(t, run.ID)
	require.Equal(t, "[Grade]", run.Metadata["references"])
	require.Equal(t, "2", run.Metadata["text-chunks"])
	input := &ReadInput{URI: uri, Representation: "metadata", ExpectedVersion: inspected.Version,
		Select: &ResourceSelection{ComponentID: run.ID}, Limits: &ResourceLimits{MaxRows: 1}}
	var output ReadOutput
	var reconstructed strings.Builder
	part := 0
	for {
		require.NoError(t, service.read(ctx, input, &output))
		require.Len(t, output.Components, 1)
		component := output.Components[0]
		if component.Kind == "metadataText" {
			part++
			require.Equal(t, run.ID, component.ParentID)
			require.Equal(t, "text", component.Name)
			require.Equal(t, strconv.Itoa(part), component.Metadata["part"])
			reconstructed.WriteString(component.Metadata["text"])
		}
		if output.Cursor == "" {
			break
		}
		input.Cursor = output.Cursor
	}
	require.Equal(t, 2, part)
	require.Equal(t, text, reconstructed.String())
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
