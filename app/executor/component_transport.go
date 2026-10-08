package executor

import (
	"context"
	"fmt"
	"github.com/viant/agently-core/service/datasource"
	"github.com/viant/authz/gating"
)

func (b *Builder) WithNativeComponentProducers(services ...string) *Builder {
	b.nativeComponentServices = append([]string(nil), services...)
	return b
}
func (b *Builder) configureComponentTransport(runtime *Runtime) error {
	if len(b.nativeComponentServices) > 0 {
		transport, ok := runtime.Registry.(datasource.NativeComponentTransport)
		if !ok {
			return fmt.Errorf("configured registry lacks native component request metadata transport")
		}
		dispatcher, err := datasource.NewNativeMCPComponentDispatcher(transport, b.nativeComponentServices)
		if err != nil {
			return err
		}
		runtime.ComponentDispatcher = dispatcher
	}
	if runtime.ComponentDispatcher == nil {
		return nil
	}
	setter, ok := runtime.Registry.(interface {
		SetComponentProducerClassifier(func(string) bool)
		SetComponentAuthorityResolver(func(context.Context) (gating.Principal, error))
	})
	if !ok {
		return fmt.Errorf("configured registry lacks component authority guard")
	}
	setter.SetComponentProducerClassifier(runtime.ComponentDispatcher.IsComponentProducer)
	setter.SetComponentAuthorityResolver(runtime.ComponentAuthority)
	return nil
}
