package llm

import (
	"errors"
	"fmt"
	"strings"
)

const unsupportedAttachmentMessage = "This attachment cannot be processed by the selected model or attachment mode."

type AttachmentCapabilityError struct {
	Filename string
	MIMEType string
	Model    string
	Mode     string
	Err      error
}

func (e *AttachmentCapabilityError) Error() string {
	if e == nil {
		return unsupportedAttachmentMessage
	}
	parts := []string{unsupportedAttachmentMessage}
	if filename := strings.TrimSpace(e.Filename); filename != "" {
		parts = append(parts, "file="+filename)
	}
	if mimeType := strings.TrimSpace(e.MIMEType); mimeType != "" {
		parts = append(parts, "mime="+mimeType)
	}
	if model := strings.TrimSpace(e.Model); model != "" {
		parts = append(parts, "model="+model)
	}
	if mode := strings.TrimSpace(e.Mode); mode != "" {
		parts = append(parts, "mode="+mode)
	}
	if e.Err != nil {
		parts = append(parts, "error="+e.Err.Error())
	}
	return strings.Join(parts, "; ")
}

func (e *AttachmentCapabilityError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *AttachmentCapabilityError) UserMessage() string {
	if e == nil {
		return ""
	}
	var details []string
	if filename := strings.TrimSpace(e.Filename); filename != "" {
		details = append(details, fmt.Sprintf("File: %s.", filename))
	}
	if mimeType := strings.TrimSpace(e.MIMEType); mimeType != "" {
		details = append(details, fmt.Sprintf("MIME type: %s.", mimeType))
	}
	if model := strings.TrimSpace(e.Model); model != "" {
		details = append(details, fmt.Sprintf("Model: %s.", model))
	}
	if mode := strings.TrimSpace(e.Mode); mode != "" {
		details = append(details, fmt.Sprintf("Attachment mode: %s.", mode))
	}
	if len(details) == 0 {
		return unsupportedAttachmentMessage
	}
	return unsupportedAttachmentMessage + "\n\n" + strings.Join(details, " ")
}

func NewAttachmentCapabilityError(filename, mimeType, model, mode string, err error) error {
	return &AttachmentCapabilityError{
		Filename: strings.TrimSpace(filename),
		MIMEType: strings.TrimSpace(mimeType),
		Model:    strings.TrimSpace(model),
		Mode:     strings.TrimSpace(mode),
		Err:      err,
	}
}

func AttachmentCapabilityUserMessage(err error) (string, bool) {
	var target *AttachmentCapabilityError
	if !errors.As(err, &target) || target == nil {
		return "", false
	}
	msg := strings.TrimSpace(target.UserMessage())
	if msg == "" {
		msg = unsupportedAttachmentMessage
	}
	return msg, true
}
