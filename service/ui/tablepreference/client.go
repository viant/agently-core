package tablepreference

import (
	"context"
	"encoding/json"
	"fmt"
)

// Transport maps get/set/reset operations to external MCP tools (or another
// host adapter). It unwraps the MCP envelope into the JSON contract below.
// Authentication, workspace authorization and persistence belong to the host.
type Transport interface {
	Call(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

type Request struct {
	Key         string       `json:"key"`
	Preferences *Preferences `json:"preferences,omitempty"`
}

type Client struct{ transport Transport }

func NewClient(transport Transport) (*Client, error) {
	if transport == nil {
		return nil, fmt.Errorf("table preferences: external transport is required")
	}
	return &Client{transport: transport}, nil
}

func (c *Client) Get(ctx context.Context, key string) (*Preferences, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	request, _ := json.Marshal(Request{Key: key})
	body, err := c.transport.Call(ctx, "get", request)
	if err != nil {
		return nil, err
	}
	var preferences *Preferences
	if err := decodeStrict(body, &preferences); err != nil {
		return nil, err
	}
	if preferences == nil {
		return nil, nil
	}
	if err := Validate(preferences); err != nil {
		return nil, err
	}
	return preferences, nil
}

func (c *Client) Set(ctx context.Context, key string, p *Preferences) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	if err := Validate(p); err != nil {
		return err
	}
	return c.write(ctx, "set", Request{Key: key, Preferences: p})
}

func (c *Client) Reset(ctx context.Context, key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	return c.write(ctx, "reset", Request{Key: key})
}

func (c *Client) write(ctx context.Context, operation string, request Request) error {
	payload, _ := json.Marshal(request)
	if len(payload) > MaxBytes {
		return fmt.Errorf("table preferences: payload exceeds contract limits")
	}
	response, err := c.transport.Call(ctx, operation, payload)
	if err != nil {
		return err
	}
	var ack struct{}
	return decodeStrict(response, &ack)
}
