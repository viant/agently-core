package narrator

import "time"

type Sink func(text string) error

type Session struct {
	debouncer  *Debouncer
	sink       Sink
	lastSent   string
	lastQueued string
}

func NewSession(window time.Duration, sink Sink) *Session {
	return &Session{
		debouncer: NewDebouncer(window),
		sink:      sink,
	}
}

func (s *Session) Start(text string) error {
	if s == nil || s.sink == nil || text == "" {
		return nil
	}
	if text == s.lastSent {
		return nil
	}
	s.lastQueued = ""
	s.lastSent = text
	return s.sink(text)
}

func (s *Session) Push(text string) error {
	if s == nil || s.sink == nil || text == "" {
		return nil
	}
	if text == s.lastSent || text == s.lastQueued {
		return nil
	}
	s.lastQueued = text
	if flushed := s.debouncer.Push(text); flushed != "" {
		if flushed == s.lastSent {
			s.lastQueued = ""
			return nil
		}
		s.lastSent = flushed
		s.lastQueued = ""
		return s.sink(flushed)
	}
	return nil
}

func (s *Session) Channel() <-chan time.Time {
	if s == nil {
		return nil
	}
	return s.debouncer.Channel()
}

func (s *Session) Flush() error {
	if s == nil || s.sink == nil {
		return nil
	}
	if text := s.debouncer.Flush(); text != "" {
		s.lastQueued = ""
		if text == s.lastSent {
			return nil
		}
		s.lastSent = text
		return s.sink(text)
	}
	return nil
}
