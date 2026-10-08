package service

// ConfigureWindowCatalog decorates the host catalog before serving requests.
// It preserves the bridge's hub, admission callbacks and instance pins. This
// startup-only hook must not be called concurrently with request handling.
func (s *Service) ConfigureWindowCatalog(decorate func(WindowDefinitionCatalog) WindowDefinitionCatalog) {
	if s != nil && s.cfg != nil && decorate != nil {
		s.cfg.WindowDefinitions = decorate(s.cfg.WindowDefinitions)
	}
}
