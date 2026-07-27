package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	intakesvc "github.com/viant/agently-core/service/intake"
)

func TestDirectActionFromContext(t *testing.T) {
	ctx := map[string]any{
		intakesvc.ContextKey: &intakesvc.Context{
			DirectAction: intakesvc.DirectActionContext{
				ToolName:      "ui/view:open",
				Input:         map[string]any{"id": "order"},
				AssistantText: "Opened the details window.",
			},
		},
	}
	got := directActionFromContext(ctx)
	if got == nil {
		t.Fatalf("expected direct action")
	}
	if got.ToolName != "ui/view:open" {
		t.Fatalf("toolName = %q", got.ToolName)
	}
}

func TestValidateDirectAction(t *testing.T) {
	ok := &intakesvc.DirectActionContext{
		ToolName:      "ui/view:open",
		Input:         map[string]any{"id": "order"},
		InputJSON:     `{"id":"order"}`,
		AssistantText: "Opened the details window.",
	}
	if err := validateDirectAction(ok); err != nil {
		t.Fatalf("expected valid direct action, got %v", err)
	}
	okRead := &intakesvc.DirectActionContext{
		ToolName:      "resources/read",
		Input:         map[string]any{"path": "/tmp/recovery.md", "rootId": "local"},
		InputJSON:     `{"path":"/tmp/recovery.md","rootId":"local"}`,
		AssistantText: "Opening the requested file for review.",
	}
	if err := validateDirectAction(okRead); err != nil {
		t.Fatalf("expected resources/read direct action to be valid, got %v", err)
	}
	bad := &intakesvc.DirectActionContext{
		ToolName:      "system/exec",
		Input:         map[string]any{"cmd": "whoami"},
		AssistantText: "no",
	}
	if err := validateDirectAction(bad); err != nil {
		t.Fatalf("expected structural validation to pass, got %v", err)
	}
	missingViewID := &intakesvc.DirectActionContext{
		ToolName:      "ui/view:open",
		Input:         map[string]any{"AdLineId": "7288336"},
		AssistantText: "Opening forecast view.",
	}
	require.ErrorContains(t, validateDirectAction(missingViewID), "input.id or input.items is required")

	multiOpen := &intakesvc.DirectActionContext{
		ToolName: "ui/view:open",
		Input: map[string]any{
			"items": []interface{}{
				map[string]interface{}{
					"id": "campaign",
					"parameters": map[string]interface{}{
						"CampaignId": []interface{}{553524},
					},
				},
			},
		},
		AssistantText: "Opening the requested campaign window.",
	}
	require.NoError(t, validateDirectAction(multiOpen))
}

func TestAuthorizeDirectAction_UsesIntakeToolItemsAndBundles(t *testing.T) {
	svc := &Service{
		registry: &fakeRegistry{defs: []llm.ToolDefinition{
			{Name: "resources/read"},
			{Name: "ui/view:open"},
			{Name: "system/exec:execute"},
		}},
		toolBundles: func(context.Context) ([]*toolbundle.Bundle, error) {
			return []*toolbundle.Bundle{
				{
					ID: "ui-direct",
					Match: []llm.Tool{
						{Name: "ui/view:open"},
					},
				},
			}, nil
		},
	}
	input := &QueryInput{
		Agent: &agentmdl.Agent{
			Intake: agentmdl.Intake{
				Tool: agentmdl.Tool{
					Bundles: []string{"ui-direct"},
					Items:   []*llm.Tool{{Name: "resources/read"}},
				},
			},
		},
	}
	require.NoError(t, svc.authorizeDirectAction(context.Background(), input, &intakesvc.DirectActionContext{
		ToolName:      "resources/read",
		Input:         map[string]any{"path": "/tmp/recovery.md"},
		AssistantText: "open",
	}))
	require.NoError(t, svc.authorizeDirectAction(context.Background(), input, &intakesvc.DirectActionContext{
		ToolName:      "ui/view:open",
		Input:         map[string]any{"id": "order"},
		AssistantText: "open",
	}))
	require.Error(t, svc.authorizeDirectAction(context.Background(), input, &intakesvc.DirectActionContext{
		ToolName:      "system/exec:execute",
		Input:         map[string]any{"cmd": "pwd"},
		AssistantText: "open",
	}))
}

func TestConversationMetadata_PreservesUnknownWorkspaceKeysInExtra(t *testing.T) {
	raw := `{"workspace":{"windowId":"order_123","windowKey":"order"},"workspaceState":{"selectedWindowId":"order_123","windows":[{"windowId":"order_123","windowKey":"order"}]}}`
	var decoded ConversationMetadata
	require.NoError(t, json.Unmarshal([]byte(raw), &decoded))
	require.Contains(t, decoded.Extra, "workspace")
	require.Contains(t, decoded.Extra, "workspaceState")
	encoded, err := json.Marshal(decoded)
	require.NoError(t, err)
	require.JSONEq(t, raw, string(encoded))
}

func TestNormalizeInterfaceMap(t *testing.T) {
	type payload struct {
		Parameters struct {
			RecordID []int `json:"RecordId"`
		} `json:"parameters"`
	}
	value := payload{}
	value.Parameters.RecordID = []int{2656980}
	got := normalizeInterfaceMap(value.Parameters)
	require.Equal(t, map[string]interface{}{
		"RecordId": []interface{}{float64(2656980)},
	}, got)
}

func TestDirectActionAssistantText_FormatsDiagnosticResult(t *testing.T) {
	action := &intakesvc.DirectActionContext{
		ToolName:      "steward/Diagnostic",
		AssistantText: directActionToolResultAssistantText,
	}
	result := `{
		"scope":{"adOrderIds":[2665777]},
		"coverage":{
			"level":"partial",
			"skippedSurfaces":["GlobalSupplyPerformance"],
			"deeperProofRequired":true
		},
		"explanation":{
			"primaryBlockerClass":"DEEPER_PROOF_REQUIRED",
			"confidence":"low",
			"diagnosis":"The available evidence does not establish one material blocker.",
			"supportingFacts":[
				{"path":"facts.delivery.missingBidCoveragePct","value":"1.14","source":"MetricsAdCube"}
			],
			"nextValidation":"Validate pricing and supply-path evidence."
		}
	}`

	text := directActionAssistantText(action, result)
	require.Contains(t, text, "```forge-report")
	require.Contains(t, text, `"grammar":"report-document-v1"`)
	require.Contains(t, text, `"title":"Primary read"`)
	require.Contains(t, text, "No single delivery constraint is proven")
	require.Contains(t, text, "The available evidence does not establish one material constraint.")
	require.Contains(t, text, `"title":"Primary supporting facts"`)
	require.Contains(t, text, "Share of the delivery gap explained by this factor")
	require.Contains(t, text, `"coverageLevel":"Partial evidence"`)
	require.Contains(t, text, "Validate pricing and inventory-path evidence.")
	require.NotContains(t, text, "DEEPER_PROOF_REQUIRED")
	require.NotContains(t, text, "facts.delivery")
	require.NotContains(t, text, "GlobalSupplyPerformance")
	require.NotContains(t, text, `"explanation"`)
}

func TestFormatDiagnosticReport_LabelsAggregateAndSnapshotSpend(t *testing.T) {
	result := `{
		"scope":{"from":"2026-07-19","to":"2026-07-25","adOrderIds":[2665777]},
		"coverage":{"level":"partial","loadedSurfaces":["MetricsAdCube"],"skippedSurfaces":["GlobalSupplyPerformance"]},
		"explanation":{
			"primaryBlockerClass":"DEEPER_PROOF_REQUIRED",
			"confidence":"low",
			"diagnosis":"No one causal blocker is proven.",
			"nextValidation":"Validate price and supply evidence."
		},
		"factDatasets":{
			"delivery_summary":{
				"columns":["bids","impressions","totalSpend","dailySpendShortfall","flightSpendShortfall"],
				"csv":"bids,impressions,totalSpend,dailySpendShortfall,flightSpendShortfall\n3231865,699114,6898.68,12333.93,133650.47\n"
			},
			"delivery_pacing":{
				"columns":["entityKind","entityId","dailyPacingStatus","flightPacingStatus","dailySpendShortfall","flightSpendShortfall","bids","impressions","totalSpend"],
				"csv":"entityKind,entityId,dailyPacingStatus,flightPacingStatus,dailySpendShortfall,flightSpendShortfall,bids,impressions,totalSpend\nad_order,2665777,behind,behind,12333.93,133650.47,371700,110900,1084\n"
			},
			"restriction_soft_ineligibilities":{
				"columns":["feature","optimizationRejections","estimatedOptimizationRejections","optimizationRejectionShare","source","classification","pacingSpendShortfall","observedSpendPerBid","approximateMissingBids","estimatedGapCoverage","countAssessment"],
				"csv":"feature,optimizationRejections,estimatedOptimizationRejections,optimizationRejectionShare,source,classification,pacingSpendShortfall,observedSpendPerBid,approximateMissingBids,estimatedGapCoverage,countAssessment\noffer.bid.floor,992276,943414,0.9507,AdTargetingProfile,primary,133650.47,0.002134,62629052,0.01506,below_materiality_threshold\n"
			}
		}
	}`

	text := formatDiagnosticToolResult(result)
	require.Contains(t, text, `"title":"Report-window spend"`)
	require.Contains(t, text, `"description":"2026-07-19 through 2026-07-25"`)
	require.Contains(t, text, `"title":"Report-window delivery aggregate — 2026-07-19 through 2026-07-25"`)
	require.Contains(t, text, `"label":"Window spend"`)
	require.Contains(t, text, `"title":"Latest entity pacing snapshot — not window totals"`)
	require.Contains(t, text, `"label":"Snapshot spend"`)
	require.Contains(t, text, "The headline spend is the **aggregate for 2026-07-19 through 2026-07-25**.")
	require.Contains(t, text, `"totalSpend":6898.68`)
	require.Contains(t, text, `"totalSpend":1084`)
	require.Contains(t, text, "Bid competitiveness against inventory price floors")
	require.Contains(t, text, "Targeting and optimization settings")
	require.Contains(t, text, "Delivery-gap materiality by factor")
	require.Contains(t, text, `"label":"Observed filter events"`)
	require.Contains(t, text, `"label":"Pacing shortfall"`)
	require.Contains(t, text, `"label":"Approx. opportunities needed"`)
	require.Contains(t, text, "Too small to explain enough of the delivery gap")
	require.NotContains(t, text, "offer.bid.floor")
	require.NotContains(t, text, "AdTargetingProfile")
	require.NotContains(t, text, `"classification"`)
	require.NotContains(t, text, `"optimizationRejectionShare"`)
	require.NotContains(t, text, `"estimatedGapCoverage"`)
	require.Contains(t, text, `"mode":"commit"`)
}

func TestFormatDiagnosticReport_ExposesSellerIDAsSupplyPathEvidence(t *testing.T) {
	result := `{
		"scope":{"from":"2026-07-19","to":"2026-07-25","adOrderIds":[2665777]},
		"coverage":{"level":"full","loadedSurfaces":["SupplyOptimizationPerformance"]},
		"explanation":{
			"primaryBlockerClass":"DEEPER_PROOF_REQUIRED",
			"confidence":"low",
			"diagnosis":"Supply-path evidence needs validation."
		},
		"factDatasets":{
			"supply_path_evidence":{
				"columns":["sellerId","sellerDomain","sellerDomainPath","publisherId","dealId","siteId","hopCount","pathComplete","bids","impressions","spend","winRate","ecpm"],
				"csv":"sellerId,sellerDomain,sellerDomainPath,publisherId,dealId,siteId,hopCount,pathComplete,bids,impressions,spend,winRate,ecpm\nseller-42,exchange.example,\"publisher.example,exchange.example\",12,34,56,2,true,1200,240,31.5,0.2,131.25\n"
			}
		}
	}`

	text := formatDiagnosticToolResult(result)
	require.Contains(t, text, `"title":"Supply-path evidence"`)
	require.Contains(t, text, `"label":"Seller ID"`)
	require.Contains(t, text, `"sellerId":"seller-42"`)
	require.Contains(t, text, "Seller ID identifies the observed supply-path participant; it is not causal proof by itself.")
	require.NotContains(t, text, `"title":"Primary diagnosis","datasetRef":"supply_path_evidence"`)
}

func TestDiagnosticClassLabel_UsesCustomerLanguage(t *testing.T) {
	require.Equal(t, "Deal eligibility is limiting available inventory", diagnosticClassLabel("DEAL_RESTRICTION_PRESSURE"))
	require.Equal(t, "Eligible site inventory is too narrow", diagnosticClassLabel("SITE_SUPPLY_RESTRICTION_PRESSURE"))
	require.Equal(t, "Recent-contact safeguards are limiting repeat opportunities", diagnosticClassLabel("RECENT_BID_SUPPRESSION_SIGNAL"))
	require.Equal(t, "Bid competitiveness may be limiting wins", diagnosticClassLabel("EFFECTIVE_BID_COMPETITIVENESS_SIGNAL"))
	require.Equal(t, "A bid-price gap is visible, but its cause needs validation", diagnosticClassLabel("BID_FLOOR_PRESSURE"))
	require.Equal(t, "Future constraint type", diagnosticClassLabel("FUTURE_PRESSURE_TYPE"))
}

func TestDiagnosticUserFacingRestrictionRows_KeepsStrongestCountPerFactor(t *testing.T) {
	rows := []map[string]interface{}{
		{"feature": "offer.bid.floor", "estimatedOptimizationRejections": 10},
		{"feature": "offer.bid.floor", "estimatedOptimizationRejections": 25},
		{"feature": "ml.fraud.filter", "estimatedOptimizationRejections": 5},
	}

	got := diagnosticUserFacingRestrictionRows(rows)

	require.Len(t, got, 2)
	require.Equal(t, "Bid competitiveness against inventory price floors", got[0]["factor"])
	require.Equal(t, 25, got[0]["estimatedAffectedOpportunities"])
	require.Equal(t, "Inventory quality and fraud safeguards", got[1]["factor"])
}

func TestDiagnosticSupportingFactRows_PrefersCountsOverRedundantRatios(t *testing.T) {
	rows := diagnosticSupportingFactRows(map[string]interface{}{
		"supportingFacts": []interface{}{
			map[string]interface{}{"path": "facts.setup.optimizationRejections[0].estimatedOptimizationRejections", "value": "2012294", "source": "AdTargetingProfile"},
			map[string]interface{}{"path": "facts.delivery.approxMissingBids", "value": "88426", "source": "MetricsAdCube"},
			map[string]interface{}{"path": "facts.delivery.missingBidCoveragePct", "value": "2275.68", "source": "MetricsAdCube"},
			map[string]interface{}{"path": "facts.delivery.deliveredBidSharePct", "value": "989.17", "source": "MetricsAdCube"},
		},
	})

	require.Len(t, rows, 2)
	require.Equal(t, "Estimated opportunities affected by the leading factor", rows[0]["path"])
	require.Equal(t, "Estimated additional opportunities needed to close the delivery gap", rows[1]["path"])
}

func TestDiagnosticCSVScalar_PreservesNumericZeroAndOne(t *testing.T) {
	require.EqualValues(t, int64(0), diagnosticCSVScalar("0"))
	require.EqualValues(t, int64(1), diagnosticCSVScalar("1"))
	require.Equal(t, false, diagnosticCSVScalar("false"))
	require.Equal(t, true, diagnosticCSVScalar("true"))
}

func TestDirectActionAssistantText_FallsBackToRawToolResult(t *testing.T) {
	action := &intakesvc.DirectActionContext{
		ToolName:      "resources/read",
		AssistantText: directActionToolResultAssistantText,
	}
	require.Equal(t, "plain tool result", directActionAssistantText(action, " plain tool result "))
}

func TestAnnotateDirectActionExecution(t *testing.T) {
	svc := &Service{}
	input := &QueryInput{
		ConversationID: "conv-1",
		MessageID:      "turn-1",
		Context: map[string]any{
			intakesvc.ContextKey: &intakesvc.Context{
				DirectAction: intakesvc.DirectActionContext{
					ToolName:      "ui/view:open",
					Input:         map[string]any{"id": "order"},
					AssistantText: "Opened order.",
				},
			},
		},
	}
	action := directActionFromContext(input.Context)
	require.NotNil(t, action)
	result := `{"ok":true,"windowId":"order_2656980"}`
	svc.annotateDirectActionExecution(input, action, &result)
	tc := intakesvc.FromContext(input.Context)
	require.NotNil(t, tc)
	require.True(t, tc.DirectActionExecution.Executed)
	require.Equal(t, "ui/view:open", tc.DirectActionExecution.ToolName)
	require.Equal(t, map[string]interface{}{"ok": true, "windowId": "order_2656980"}, tc.DirectActionExecution.Result)
	require.Equal(t, `{"ok":true,"windowId":"order_2656980"}`, tc.DirectActionExecution.ResultText)
	require.Equal(t, true, input.Context["intake.directActionExecuted"])
	require.Equal(t, "ui/view:open", input.Context["intake.directActionTool"])
}

func TestPublishDirectActionAssistantMessage_WritesCompletedStatus(t *testing.T) {
	recorder := &intakeRecordingConvClient{}
	svc := &Service{conversation: recorder}
	input := &QueryInput{
		ConversationID: "conv-1",
		MessageID:      "turn-1",
	}
	err := svc.publishDirectActionAssistantMessage(context.Background(), input, "Opened the details window.")
	require.NoError(t, err)
	require.NotNil(t, recorder.lastMessage)
	require.NotNil(t, recorder.lastMessage.Status)
	require.Equal(t, "completed", *recorder.lastMessage.Status)
	require.NotNil(t, recorder.lastMessage.Content)
	require.Equal(t, "Opened the details window.", *recorder.lastMessage.Content)
	require.True(t, recorder.lastMessageAdd)
}

func TestMaybeRunDirectAction_InvalidActionFallsThrough(t *testing.T) {
	svc := &Service{}
	input := &QueryInput{
		ConversationID: "conv-1",
		MessageID:      "turn-1",
		Context: map[string]any{
			intakesvc.ContextKey: &intakesvc.Context{
				Prompting: intakesvc.PromptingContext{
					SuggestedProfileID: "workspace_console",
				},
				DirectAction: intakesvc.DirectActionContext{
					ToolName:      "ui/view:open",
					Input:         map[string]any{"AdLineId": "7288336"},
					AssistantText: "Opening Review window.",
				},
			},
		},
	}
	output := &QueryOutput{}

	handled, err := svc.maybeRunDirectAction(context.Background(), input, output)
	require.NoError(t, err)
	require.False(t, handled)

	tc := intakesvc.FromContext(input.Context)
	require.NotNil(t, tc)
	require.Empty(t, tc.DirectAction.ToolName)
	require.Equal(t, "workspace_console", tc.Prompting.SuggestedProfileID)
}

func TestDiagnosticEvidenceLabel_CampaignIncidentFacts(t *testing.T) {
	require.Equal(t, "Previous-day order spend", diagnosticEvidenceLabel("facts.baseline.previousOrder.spend"))
	require.Equal(t, "Current-day order spend", diagnosticEvidenceLabel("facts.baseline.currentOrder.spend"))
	require.Equal(t, "Sibling-order spend gained", diagnosticEvidenceLabel("facts.baseline.campaignContext.siblingSpendGain"))
	require.Equal(t, "Sibling-order spend lost", diagnosticEvidenceLabel("facts.baseline.campaignContext.siblingSpendLoss"))
	require.Equal(t, "Campaign pacing reallocation detected", diagnosticEvidenceLabel("facts.baseline.campaignContext.pacing.hasReallocationSignal"))
}
