package resources

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxTableauDepth      = 256
	maxTableauComponents = 50000
	maxTableauValue      = 512
	tableauTextChunkSize = 4096
)

type tableauElement struct {
	kind, attributes, textKey string
}

// Capture source structure through an allowlist. Workbook nodes also contain
// credentials, inline data, script bodies and other private XML.
var tableauElements = map[string]tableauElement{
	"datasource":              {"datasource", "name caption version", ""},
	"datasource-dependencies": {"dependency", "datasource", ""},
	"connection":              {"connection", "class dbname catalog schema", ""},
	"named-connection":        {"connection", "name caption", ""},
	"relation":                {"relation", "name type connection table join", "sql"},
	"relationship":            {"relationship", "name type", ""},
	"object":                  {"relationObject", "id caption", ""},
	"first-end-point":         {"endpoint", "object-id", ""},
	"second-end-point":        {"endpoint", "object-id", ""},
	"clause":                  {"clause", "type", ""},
	"expression":              {"expression", "op", ""},
	"extract":                 {"extract", "enabled", ""},
	"metadata-record":         {"sourceField", "class", ""},
	"remote-name":             {"property", "", "value"},
	"local-name":              {"property", "", "value"},
	"parent-name":             {"property", "", "value"},
	"local-type":              {"property", "", "value"},
	"remote-type":             {"property", "", "value"},
	"aggregation":             {"property", "", "value"},
	"column":                  {"field", "name caption datatype role type aggregation default-format param-domain-type", ""},
	"column-instance":         {"fieldInstance", "name column derivation type", ""},
	"calculation":             {"calculation", "class", ""},
	"group":                   {"datasourceGroup", "name caption hidden auto-column name-style ui-vit-column", ""},
	"aliases":                 {"aliases", "enabled", ""},
	"alias":                   {"alias", "key value", ""},
	"table-calc":              {"tableCalculation", "type aggregation ordering-type ordering-field restart", ""},
	"partition":               {"partition", "field column type", ""},
	"addressing":              {"addressing", "field column type", ""},
	"range":                   {"parameterRange", "min max granularity", ""},
	"member":                  {"parameterMember", "value alias", ""},
	"filter":                  {"filter", "class column kind included-values context in-context type", ""},
	"groupfilter":             {"filterGroup", "function level member", ""},
	"min":                     {"filterBound", "", "value"},
	"max":                     {"filterBound", "", "value"},
	"worksheet":               {"worksheet", "name", ""},
	"rows":                    {"shelf", "", "expression"},
	"cols":                    {"shelf", "", "expression"},
	"pane":                    {"pane", "id x-axis-name y-axis-name x-index y-index", ""},
	"mark":                    {"mark", "class", ""},
	"shelf-sort-v2":           {"shelfSort", "dimension-to-sort measure-to-sort-by direction shelf is-on-innermost-dimension", ""},
	"manual-sort":             {"manualSort", "column direction", ""},
	"bucket":                  {"sortBucket", "", "value"},
	"customized-tooltip":      {"tooltip", "enabled show-buttons", ""},
	"customized-label":        {"label", "", ""},
	"layout-options":          {"layoutOptions", "", ""},
	"title":                   {"title", "", ""},
	"formatted-text":          {"formattedText", "", ""},
	"run":                     {"textRun", "bold italic underline fontalignment fontcolor fontname fontsize", "text"},
	"dashboard":               {"dashboard", "name", ""},
	"zone":                    {"zone", "id name type-v2 param x y w h", ""},
	"size":                    {"layoutSize", "sizing-mode minwidth minheight maxwidth maxheight", ""},
	"action":                  {"action", "name caption type", ""},
	"activation":              {"actionActivation", "type auto-clear", ""},
	"source":                  {"actionSource", "worksheet dashboard type", ""},
	"target":                  {"actionTarget", "worksheet dashboard type", ""},
	"command":                 {"actionCommand", "command type", ""},
	"param":                   {"actionParameter", "name", ""},
	"window":                  {"tab", "name class maximized", ""},
	"shared-view":             {"sharedView", "name", ""},
	"repository-location":     {"repositoryLocation", "id revision", ""},
}

var tableauContainers = map[string]bool{
	"actions": true, "dashboards": true, "datasources": true, "encodings": true, "metadata-records": true,
	"members": true, "named-connections": true, "object-graph": true, "objects": true, "panes": true,
	"properties": true, "relationships": true, "shared-views": true, "shelf-sorts": true, "table": true, "view": true,
	"windows": true, "worksheets": true, "zones": true,
}

type tableauFrame struct {
	tag       string
	parent    string
	component int
	ignored   bool
	textKey   string
	text      []byte
	runs      []tableauTextRun
}

type tableauTextRun struct {
	component  int
	start, end int
}

type tableauDefinitionReader struct {
	ctx    context.Context
	source io.Reader
}

func (r *tableauDefinitionReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(data)
}

func parseTableauMetadata(ctx context.Context, source io.Reader, name string, hasExtract bool) (*tableauMetadata, error) {
	limited := &io.LimitedReader{R: &tableauDefinitionReader{ctx: ctx, source: source}, N: maxTableauDefinitionBytes + 1}
	decoder := xml.NewDecoder(limited)
	result := &tableauMetadata{properties: map[string]string{"definition": name, "hasExtract": strconv.FormatBool(hasExtract)}}
	var frames []tableauFrame
	var roots, unmodeled int
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		token, err := decoder.Token()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if limited.N == 0 {
			return nil, fmt.Errorf("Tableau definition exceeds inspection limit")
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid Tableau workbook XML")
		}
		switch value := token.(type) {
		case xml.Directive:
			return nil, fmt.Errorf("Tableau workbook XML directives are unsupported")
		case xml.StartElement:
			if len(frames) == maxTableauDepth {
				return nil, fmt.Errorf("Tableau definition exceeds nesting limit")
			}
			tag := value.Name.Local
			if len(frames) == 0 {
				roots++
				if roots != 1 || tag != "workbook" {
					return nil, fmt.Errorf("expected a single Tableau workbook root")
				}
				for _, key := range []string{"name", "version", "original-version", "source-build", "source-platform"} {
					tableauSet(result.properties, key, tableauAttribute(value.Attr, key))
				}
				frames = append(frames, tableauFrame{tag: tag, component: -1})
				continue
			}
			parent := frames[len(frames)-1]
			frame := tableauFrame{tag: tag, component: -1, parent: parent.parent, ignored: parent.ignored}
			if !frame.ignored {
				element, found := tableauElements[tag]
				switch {
				case parent.tag == "encodings":
					element, found = tableauElement{"encoding", "column", ""}, true
				case tag == "datasource" && !(len(frames) == 2 && parent.tag == "datasources"):
					element.kind = "datasourceReference"
				case tag == "column" && tableauAttribute(value.Attr, "param-domain-type") != "":
					element.kind = "parameter"
					element.attributes += " value"
				case tag == "group":
					found = parent.tag == "datasource"
				case tag == "alias":
					found = parent.tag == "aliases"
				case tag == "bucket":
					found = parent.tag == "dictionary" && result.componentKind(parent.parent) == "manualSort"
				case tag == "run":
					found = parent.tag == "formatted-text"
				case tag == "param":
					found = parent.tag == "command"
					switch tableauAttribute(value.Attr, "name") {
					case "target", "source", "target-field", "source-field", "field", "clear", "mode":
						element.attributes += " value"
					}
				}
				if found {
					if len(result.components) == maxTableauComponents {
						return nil, fmt.Errorf("Tableau definition exceeds component limit")
					}
					frame.component = len(result.components)
					id := "tableau-" + strconv.Itoa(frame.component+1)
					properties := map[string]string{}
					for _, key := range strings.Fields(element.attributes) {
						tableauSet(properties, key, tableauAttribute(value.Attr, key))
					}
					unmodeledAttributes := 0
					for _, attribute := range value.Attr {
						if !strings.Contains(" "+element.attributes+" ", " "+attribute.Name.Local+" ") &&
							!(tag == "calculation" && attribute.Name.Local == "formula") {
							unmodeledAttributes++
						}
					}
					if unmodeledAttributes != 0 {
						properties["unmodeledAttributes"] = strconv.Itoa(unmodeledAttributes)
					}
					label := properties["name"]
					if label == "" {
						label = tag
					}
					if tag == "relation" && properties["type"] != "text" {
						element.textKey = ""
						switch properties["type"] {
						case "table", "join", "union", "collection":
						default:
							properties["unsupported"] = "relation type"
						}
					}
					if tag == "extract" || tag == "connection" && (properties["class"] == "hyper" || properties["class"] == "dataengine") {
						result.properties["hasExtract"] = "true"
					}
					if tag == "filter" {
						properties["scope"] = result.filterScope(frame.parent)
					}
					result.components = append(result.components, ResourceComponent{ID: id, ParentID: frame.parent, Name: label, Kind: element.kind, Metadata: properties})
					frame.parent, frame.textKey = id, element.textKey
					for _, key := range strings.Fields(element.attributes) {
						rawValue := tableauAttribute(value.Attr, key)
						if len(rawValue) > maxTableauValue && properties["omitted-"+key] == "exceeds inspection limit" {
							delete(properties, "omitted-"+key)
							if err := result.setText(ctx, frame.component, key, rawValue); err != nil {
								return nil, err
							}
						}
					}
					if tag == "calculation" {
						formula := tableauAttribute(value.Attr, "formula")
						if err := result.setText(ctx, frame.component, "formula", formula); err != nil {
							return nil, err
						}
						if properties["omitted-formula"] == "" {
							if err := result.setText(ctx, frame.component, "references", tableauReferences(formula)); err != nil {
								return nil, err
							}
						}
					}
				} else {
					knownContainer := tableauContainers[tag] || tag == "dictionary" && parent.tag == "manual-sort" ||
						tag == "default-sorts" && parent.tag == "datasource"
					frame.ignored = !knownContainer
					if frame.ignored {
						unmodeled++
					}
				}
			}
			frames = append(frames, frame)
		case xml.CharData:
			if len(frames) == 0 {
				if text := bytes.TrimSpace(value); len(text) != 0 && !(roots == 0 && bytes.Equal(text, []byte("\xef\xbb\xbf"))) {
					return nil, fmt.Errorf("unexpected content outside Tableau workbook")
				}
				continue
			}
			frame := &frames[len(frames)-1]
			if frame.textKey != "" {
				frame.text = append(frame.text, value...)
			}
		case xml.EndElement:
			if len(frames) == 0 {
				return nil, fmt.Errorf("invalid Tableau workbook XML")
			}
			frame := frames[len(frames)-1]
			frames = frames[:len(frames)-1]
			if frame.tag == "run" && frame.textKey == "text" {
				formatted := &frames[len(frames)-1]
				start := len(formatted.text)
				formatted.text = append(formatted.text, frame.text...)
				formatted.runs = append(formatted.runs, tableauTextRun{component: frame.component, start: start, end: len(formatted.text)})
				continue
			}
			if len(frame.runs) != 0 {
				if err := result.setFormattedText(ctx, frame); err != nil {
					return nil, err
				}
			}
			if frame.textKey != "" {
				if err := result.setText(ctx, frame.component, frame.textKey, string(frame.text)); err != nil {
					return nil, err
				}
			}
		}
	}
	if roots != 1 || len(frames) != 0 {
		return nil, fmt.Errorf("expected a single Tableau workbook root")
	}
	if unmodeled != 0 {
		result.properties["unmodeledElements"] = strconv.Itoa(unmodeled)
	}
	return result, ctx.Err()
}

func tableauAttribute(attributes []xml.Attr, key string) string {
	for _, attribute := range attributes {
		if attribute.Name.Space == "" && attribute.Name.Local == key {
			return attribute.Value
		}
	}
	return ""
}

var tableauCredentialText = regexp.MustCompile(`(?i)(?:[a-z][a-z0-9+.-]*://[^/\s@]+@|\b(?:password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key|access[_-]?token|refresh[_-]?token|oauth[_-]?token|client[_-]?secret|authorization)["']?\s*(?:[=:]|%3d)|\bidentified\s+by\b|-----BEGIN [A-Z ]*PRIVATE KEY-----)`)

func tableauSet(properties map[string]string, key, value string) {
	switch {
	case value == "":
	case tableauCredentialText.MatchString(value):
		properties["omitted-"+key] = "credential-like value"
	case len(value) > maxTableauValue:
		properties["omitted-"+key] = "exceeds inspection limit"
	case utf8.ValidString(value):
		properties[key] = value
	}
}

// Preserve long source expressions as ordered child records so the ordinary
// component cursor can return every byte within a bounded response.
func (m *tableauMetadata) setText(ctx context.Context, component int, key, value string) error {
	parent := m.components[component]
	if tableauCredentialText.MatchString(value) {
		parent.Metadata["omitted-"+key] = "credential-like value"
		return nil
	}
	inlineLimit := maxTableauValue
	if key == "sql" || key == "formula" {
		inlineLimit = tableauTextChunkSize
	}
	if len(value) <= inlineLimit {
		if value != "" {
			parent.Metadata[key] = value
		}
		return nil
	}
	chunk := 0
	for len(value) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(m.components) == maxTableauComponents {
			return fmt.Errorf("Tableau definition exceeds component limit")
		}
		end := min(len(value), tableauTextChunkSize)
		for end < len(value) && !utf8.RuneStart(value[end]) {
			end--
		}
		chunk++
		id := "tableau-" + strconv.Itoa(len(m.components)+1)
		properties := map[string]string{"part": strconv.Itoa(chunk), "text": value[:end]}
		m.components = append(m.components, ResourceComponent{ID: id, ParentID: parent.ID, Kind: "metadataText", Name: key, Metadata: properties})
		value = value[end:]
	}
	parent.Metadata[key+"-chunks"] = strconv.Itoa(chunk)
	return nil
}

func (m *tableauMetadata) setFormattedText(ctx context.Context, frame tableauFrame) error {
	// Style changes can split a credential marker from its value, or split
	// the marker itself. Check the whole display text before releasing any run.
	redact := tableauCredentialText.Match(frame.text)
	for _, run := range frame.runs {
		if redact {
			break
		}
		redact = tableauCredentialText.Match(frame.text[run.start:run.end])
	}
	if redact {
		m.components[frame.component].Metadata["omitted-text"] = "credential-like value"
	}
	for _, run := range frame.runs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if redact {
			m.components[run.component].Metadata["omitted-text"] = "credential-like value"
			continue
		}
		text := string(frame.text[run.start:run.end])
		if err := m.setText(ctx, run.component, "text", text); err != nil {
			return err
		}
		if err := m.setText(ctx, run.component, "references", tableauTextReferences(text)); err != nil {
			return err
		}
	}
	return nil
}

func (m *tableauMetadata) componentKind(id string) string {
	index, err := strconv.Atoi(strings.TrimPrefix(id, "tableau-"))
	if err != nil || index < 1 || index > len(m.components) {
		return ""
	}
	return m.components[index-1].Kind
}

func (m *tableauMetadata) filterScope(parent string) string {
	switch kind := m.componentKind(parent); kind {
	case "datasource", "worksheet", "sharedView":
		return kind
	default:
		return "other"
	}
}

var tableauTextPlaceholder = regexp.MustCompile(`<\[[^\[\]<>\r\n]+\](?:\.\[[^\[\]<>\r\n]+\])*>`)

// Only angle-delimited Tableau field spellings are references in display text.
func tableauTextReferences(text string) string {
	seen := map[string]bool{}
	var references []string
	for {
		match := tableauTextPlaceholder.FindStringIndex(text)
		if match == nil {
			break
		}
		reference := text[match[0]+1 : match[1]-1]
		if !seen[reference] {
			seen[reference] = true
			references = append(references, reference)
		}
		text = text[match[1]:]
	}
	return strings.Join(references, ", ")
}

// A formula's bracket references are descriptive, not resolved fields.
// Ignore quoted literals and comments, and retain each spelling once.
func tableauReferences(formula string) string {
	var references []string
	seen := map[string]bool{}
	for i := 0; i < len(formula); i++ {
		switch formula[i] {
		case '\'', '"':
			quote := formula[i]
			for i++; i < len(formula); i++ {
				if formula[i] == '\\' {
					i++
				} else if formula[i] == quote {
					break
				}
			}
		case '/':
			if strings.HasPrefix(formula[i:], "//") {
				end := strings.IndexByte(formula[i:], '\n')
				if end == -1 {
					return strings.Join(references, ", ")
				}
				i += end
			} else if strings.HasPrefix(formula[i:], "/*") {
				end := strings.Index(formula[i+2:], "*/")
				if end == -1 {
					return strings.Join(references, ", ")
				}
				i += end + 3
			}
		case '[':
			end := strings.IndexByte(formula[i:], ']')
			if end < 0 {
				return strings.Join(references, ", ")
			}
			end += i + 1
			for strings.HasPrefix(formula[end:], ".[") {
				next := strings.IndexByte(formula[end+2:], ']')
				if next < 0 {
					break
				}
				end += next + 3
			}
			reference := formula[i:end]
			if !seen[reference] {
				seen[reference] = true
				references = append(references, reference)
			}
			i = end - 1
		}
	}
	return strings.Join(references, ", ")
}
