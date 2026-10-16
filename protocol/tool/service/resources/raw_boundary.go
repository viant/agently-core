package resources

func (s *Service) checkRawResourceURI(uri string, recursive bool) error {
	if s == nil || s.rawResourceBoundary == nil {
		return nil
	}
	return s.rawResourceBoundary.CheckURI(uri, recursive)
}
