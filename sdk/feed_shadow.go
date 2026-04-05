package sdk

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/viant/agently-core/internal/feedextract"
)

func toGenericFeedSpec(spec *FeedSpec) *feedextract.Spec {
	if spec == nil || len(spec.DataSource) == 0 {
		return nil
	}
	out := &feedextract.Spec{
		ID:          strings.TrimSpace(spec.ID),
		DataSources: map[string]*feedextract.DataSource{},
	}
	for name, raw := range spec.DataSource {
		if strings.TrimSpace(name) == "" || raw == nil {
			continue
		}
		data, err := json.Marshal(raw)
		if err != nil {
			continue
		}
		var ds feedextract.DataSource
		if err = json.Unmarshal(data, &ds); err != nil {
			continue
		}
		ds.Name = name
		out.DataSources[name] = &ds
	}
	if len(out.DataSources) == 0 {
		return nil
	}
	return out
}

func extractFeedData(spec *FeedSpec, requestPayloads, responsePayloads []string) (*feedextract.Result, error) {
	feedSpec := toGenericFeedSpec(spec)
	if feedSpec == nil {
		return nil, nil
	}
	return feedextract.Extract(&feedextract.Input{
		Spec:             feedSpec,
		RequestPayloads:  requestPayloads,
		ResponsePayloads: responsePayloads,
	})
}

func normalizeJSON(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	var pretty bytes.Buffer
	if err = json.Indent(&pretty, data, "", "  "); err != nil {
		return string(data)
	}
	return pretty.String()
}
