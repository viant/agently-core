package narrator

import "time"

type Sink func(text string) error

type Session struct {
	debouncer *Debouncer
	sink      Sink
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
	return s.sink(text)
}

func (s *Session) Push(text string) error {
	if s == nil || s.sink == nil || text == "" {
		return nil
	}
	if flushed := s.debouncer.Push(text); flushed != "" {
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
		return s.sink(text)
	}
	return nil
}
