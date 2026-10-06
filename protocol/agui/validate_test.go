package agui

import (
	"strings"
	"testing"
)

const validInput = `{"threadId":"thread","runId":"run","messages":[{"id":"user","role":"user","content":"hello"}]}`

func TestValidatePinnedInputSchema(t *testing.T) {
	for _, data := range []string{validInput, `{"threadId":"thread","runId":"run","messages":[]}`, strings.TrimSuffix(validInput, "}") + `,"tools":[],"context":[],"state":{},"forwardedProps":{"agently":{"version":"1","operation":"chat"}}}`} {
		if err := ValidateInput([]byte(data)); err != nil {
			t.Errorf("valid input rejected: %v", err)
		}
	}
}
func TestValidateInputRejectsExactObjectAndNullViolations(t *testing.T) {
	bad := []string{`null`, `{}`, validInput + ` {}`, strings.TrimSuffix(validInput, "}") + `,"unknown":true}`, strings.Replace(validInput, `"role":"user"`, `"role":"invalid"`, 1), strings.Replace(validInput, `"content":"hello"`, `"content":null`, 1), strings.Replace(validInput, `"content":"hello"`, `"content":"hello","extra":1`, 1)}
	for _, key := range []string{"protocolVersion", "parentRunId", "tools", "context", "resume"} {
		bad = append(bad, strings.TrimSuffix(validInput, "}")+`,"`+key+`":null}`)
	}
	for _, data := range bad {
		if err := ValidateInput([]byte(data)); err == nil {
			t.Errorf("invalid input accepted: %s", data)
		}
	}
}
