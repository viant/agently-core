package executor

// Deprecated: producer classification does not impose a client component pin
// on ordinary MCP tools. Keep startup compatibility for previously configured
// hosts; provider-owned resources select and authorize revisions themselves.
func (b *Builder) WithNativeComponentProducers(services ...string) *Builder {
	return b
}

func (b *Builder) configureComponentTransport(runtime *Runtime) error {
	return nil
}
