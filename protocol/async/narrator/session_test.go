package narrator

import (
	"testing"
	"time"
)

func TestSession_StartPushFlush(t *testing.T) {
	var got []string
	s := NewSession(10*time.Millisecond, func(text string) error {
		got = append(got, text)
		return nil
	})
	if err := s.Start("start"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if len(got) != 1 || got[0] != "start" {
		t.Fatalf("start sink = %#v", got)
	}
	if err := s.Push("a"); err != nil {
		t.Fatalf("Push(a) error = %v", err)
	}
	if err := s.Push("b"); err != nil {
		t.Fatalf("Push(b) error = %v", err)
	}
	time.Sleep(12 * time.Millisecond)
	if err := s.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if len(got) != 2 || got[1] != "b" {
		t.Fatalf("flush sink = %#v", got)
	}
}
