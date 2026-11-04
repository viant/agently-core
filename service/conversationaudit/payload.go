package conversationaudit

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"io"
	"strings"
	"unicode/utf8"
)

const MaxPayloadBytes = 2 << 20

type Payload struct {
	ID        string `json:"id"`
	MimeType  string `json:"mimeType"`
	Available bool   `json:"available"`
	Redacted  bool   `json:"redacted"`
	Body      string `json:"body,omitempty"`
	Encoding  string `json:"encoding,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

func (s *Service) Payload(ctx context.Context, conversationID, turnID, payloadID string) (*Payload, error) {
	if payloadID == "" || len(payloadID) > 256 {
		return nil, ErrFilter
	}
	d, err := s.Detail(ctx, conversationID, turnID)
	if err != nil {
		return nil, err
	}
	belongs := false
	match := func(ids ...*string) {
		for _, id := range ids {
			if id != nil && *id == payloadID {
				belongs = true
			}
		}
	}
	for _, m := range d.Row.Message {
		match(m.AttachmentPayloadId, m.ElicitationPayloadId)
		if c := m.ModelCall; c != nil {
			match(c.RequestPayloadId, c.ResponsePayloadId, c.ProviderRequestPayloadId, c.ProviderResponsePayloadId, c.StreamPayloadId)
		}
		if c := m.MessageToolCall; c != nil {
			match(c.RequestPayloadId, c.ResponsePayloadId)
		}
	}
	if !belongs {
		return nil, ErrNotFound
	}
	if s.payload == nil {
		return &Payload{ID: payloadID, Reason: "Saved payload reader unavailable."}, nil
	}
	p, err := s.payload(ctx, payloadID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, ErrNotFound
	}
	result := &Payload{ID: payloadID, MimeType: p.MimeType, Redacted: p.Redacted != nil && *p.Redacted != 0}
	if p.InlineBody == nil {
		result.Reason = "No inline payload body is saved. External references are not fetched by audit."
		return result, nil
	}
	if len(*p.InlineBody) > MaxPayloadBytes {
		return nil, ErrTooLarge
	}
	body := []byte(*p.InlineBody)
	switch strings.ToLower(p.Compression) {
	case "", "none", "identity":
	case "gzip":
		reader, e := gzip.NewReader(bytes.NewReader(body))
		if e != nil {
			result.Reason = "Saved compressed payload is unavailable."
			return result, nil
		}
		body, e = io.ReadAll(io.LimitReader(reader, MaxPayloadBytes+1))
		_ = reader.Close()
		if e != nil {
			result.Reason = "Saved compressed payload is unavailable."
			return result, nil
		}
	default:
		result.Reason = "Unsupported saved compression."
		return result, nil
	}
	if len(body) > MaxPayloadBytes {
		return nil, ErrTooLarge
	}
	result.Available = true
	if utf8.Valid(body) {
		result.Body = string(body)
		result.Encoding = "utf-8"
	} else {
		result.Body = base64.StdEncoding.EncodeToString(body)
		result.Encoding = "base64"
	}
	return result, nil
}
