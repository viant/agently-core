package datasource

import (
	"context"
	"encoding/json"
	"fmt"
	windowprotocol "github.com/viant/agently-core/protocol/window"
	"strings"
)

// NativeComponentTransport preserves normal tool admission while observing real
// tools/list metadata and sending the exact supported tools/call request _meta.
type NativeComponentTransport interface {
	ObserveNativeComponent(context.Context, string, string) (windowprotocol.ComponentBinding, error)
	ExecuteNativeComponent(context.Context, string, string, windowprotocol.ComponentBinding, map[string]interface{}) (json.RawMessage, error)
}
type NativeMCPComponentDispatcher struct {
	transport NativeComponentTransport
	services  map[string]bool
}

func NewNativeMCPComponentDispatcher(transport NativeComponentTransport, services []string) (*NativeMCPComponentDispatcher, error) {
	if transport == nil || len(services) == 0 {
		return nil, fmt.Errorf("native component transport and declared producers required")
	}
	d := &NativeMCPComponentDispatcher{transport: transport, services: map[string]bool{}}
	for _, service := range services {
		if service == "" || strings.TrimSpace(service) != service || d.services[service] {
			return nil, fmt.Errorf("native producer names must be canonical")
		}
		d.services[service] = true
	}
	return d, nil
}
func (d *NativeMCPComponentDispatcher) IsComponentProducer(service string) bool {
	return d != nil && d.services[service]
}
func (d *NativeMCPComponentDispatcher) ObserveComponent(ctx context.Context, service, method string, expected windowprotocol.ComponentBinding) (windowprotocol.ComponentBinding, error) {
	if !d.IsComponentProducer(service) {
		return windowprotocol.ComponentBinding{}, fmt.Errorf("undeclared native producer")
	}
	actual, err := d.transport.ObserveNativeComponent(ctx, service, method)
	if err != nil {
		return windowprotocol.ComponentBinding{}, err
	}
	if err = windowprotocol.ValidateComponentDispatch(&expected, actual); err != nil {
		return windowprotocol.ComponentBinding{}, err
	}
	return actual, nil
}
func (d *NativeMCPComponentDispatcher) ExecuteComponent(ctx context.Context, service, method string, pin windowprotocol.ComponentBinding, args map[string]interface{}) (json.RawMessage, error) {
	if _, err := d.ObserveComponent(ctx, service, method, pin); err != nil {
		return nil, err
	}
	result, err := d.transport.ExecuteNativeComponent(ctx, service, method, pin, args)
	if err != nil {
		return nil, err
	}
	if _, err = d.ObserveComponent(ctx, service, method, pin); err != nil {
		return nil, err
	}
	return result, nil
}

var _ windowprotocol.ComponentDispatcher = (*NativeMCPComponentDispatcher)(nil)
