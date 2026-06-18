package scheduler

import "testing"

func TestToMutableSchedule_UpdateClearsOptionalURLFields(t *testing.T) {
	blankUserCredURL := "   "
	blankTaskPromptURI := ""

	mut := toMutableSchedule(&Schedule{
		ID:            "sched-1",
		Name:          "nightly",
		AgentRef:      "chatter",
		ScheduleType:  "cron",
		Timezone:      "UTC",
		UserCredURL:   &blankUserCredURL,
		TaskPromptURI: &blankTaskPromptURI,
	}, true)

	if mut.Has == nil {
		t.Fatalf("expected set markers")
	}
	if !mut.Has.UserCredURL {
		t.Fatalf("expected UserCredURL to be marked as updated")
	}
	if mut.UserCredURL == nil || *mut.UserCredURL != "" {
		t.Fatalf("expected blank UserCredURL, got %#v", mut.UserCredURL)
	}
	if !mut.Has.TaskPromptUri {
		t.Fatalf("expected TaskPromptUri to be marked as updated")
	}
	if mut.TaskPromptUri == nil || *mut.TaskPromptUri != "" {
		t.Fatalf("expected blank TaskPromptUri, got %#v", mut.TaskPromptUri)
	}
}

func TestToMutableSchedule_CreateIgnoresBlankOptionalURLFields(t *testing.T) {
	blank := ""

	mut := toMutableSchedule(&Schedule{
		ID:            "sched-1",
		Name:          "nightly",
		AgentRef:      "chatter",
		ScheduleType:  "cron",
		Timezone:      "UTC",
		UserCredURL:   &blank,
		TaskPromptURI: &blank,
	}, false)

	if mut.Has != nil && mut.Has.UserCredURL {
		t.Fatalf("expected blank UserCredURL to be ignored on create")
	}
	if mut.UserCredURL != nil {
		t.Fatalf("expected nil UserCredURL on create, got %#v", mut.UserCredURL)
	}
	if mut.Has != nil && mut.Has.TaskPromptUri {
		t.Fatalf("expected blank TaskPromptUri to be ignored on create")
	}
	if mut.TaskPromptUri != nil {
		t.Fatalf("expected nil TaskPromptUri on create, got %#v", mut.TaskPromptUri)
	}
}
