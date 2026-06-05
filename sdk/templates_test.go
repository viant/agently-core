package sdk

import (
	"context"
	"testing"

	"github.com/viant/agently-core/workspace"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
)

func TestBackendClient_TemplatesUseWorkspaceRepository(t *testing.T) {
	ctx := context.Background()
	store := fsstore.New(t.TempDir())
	body := []byte(`
id: brief
name: brief
description: Summary
format: markdown
instructions: Use bullets
fences:
  - lang: json
    required: true
examples:
  - title: Demo
    fences:
      - lang: json
        body:
          type: report
`)
	if err := store.Save(ctx, workspace.KindTemplate, "brief", body); err != nil {
		t.Fatalf("save template: %v", err)
	}

	client := &backendClient{store: store}
	listOut, err := client.ListTemplates(ctx, &ListTemplatesInput{})
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if len(listOut.Items) != 1 || listOut.Items[0].Name != "brief" || listOut.Items[0].Format != "markdown" {
		t.Fatalf("unexpected list output: %#v", listOut)
	}

	includeDocument := true
	getOut, err := client.GetTemplate(ctx, &GetTemplateInput{Name: "brief", IncludeDocument: &includeDocument})
	if err != nil {
		t.Fatalf("GetTemplate: %v", err)
	}
	if getOut.Name != "brief" || getOut.Instructions != "Use bullets" || !getOut.IncludedDocument {
		t.Fatalf("unexpected get output: %#v", getOut)
	}
	if len(getOut.Fences) != 1 || getOut.Fences[0]["lang"] != "json" {
		t.Fatalf("unexpected fences: %#v", getOut.Fences)
	}
	if len(getOut.Examples) != 1 || getOut.Examples[0]["title"] != "Demo" {
		t.Fatalf("unexpected examples: %#v", getOut.Examples)
	}
}
