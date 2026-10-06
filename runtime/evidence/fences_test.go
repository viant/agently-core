package evidence

import (
	"fmt"
	"strings"
	"testing"
)

func TestFenceStreamEveryByteSplitKeepsUnverifiedFencePrivate(t *testing.T) {
	input := "Progress café\n```forge-data\n{\"untrusted\":true}\n```\nDone ✓"
	expected := "Progress café\n```forge-data\n{\"verified\":true}\n```\nDone ✓"
	for split := 0; split <= len(input); split++ {
		calls := 0
		stream := NewFenceStream(func(kind, body string) (string, error) {
			calls++
			if kind != "forge-data" || body != `{"untrusted":true}` {
				t.Fatal(kind, body)
			}
			return `{"verified":true}`, nil
		})
		first, err := stream.Push(input[:split], false)
		if err != nil {
			t.Fatal(split, err)
		}
		second, err := stream.Push(input[split:], true)
		if err != nil {
			t.Fatal(split, err)
		}
		if first+second != expected || strings.Contains(first, "untrusted") || calls != 1 {
			t.Fatalf("split %d leaked or changed data", split)
		}
	}
	stream := NewFenceStream(func(_, body string) (string, error) { return body, nil })
	var got strings.Builder
	for _, b := range []byte(input) {
		piece, err := stream.Push(string([]byte{b}), false)
		if err != nil {
			t.Fatal(err)
		}
		got.WriteString(piece)
	}
	last, err := stream.Push("", true)
	if err != nil {
		t.Fatal(err)
	}
	got.WriteString(last)
	if got.String() != input {
		t.Fatal("bytewise unicode stream changed")
	}
}

func TestFenceStreamRejectionAndCancellationNeverReleaseBufferedData(t *testing.T) {
	stream := NewFenceStream(func(string, string) (string, error) { return "", fmt.Errorf("wrong date binding") })
	first, err := stream.Push("Progress\n```forge-data\n{\"data\":[1]}", false)
	if err != nil || first != "Progress\n" {
		t.Fatal(first, err)
	}
	// Cancellation at this point discards stream; no data has escaped.
	rest, err := stream.Push("\n```\n", false)
	if err == nil || rest != "" {
		t.Fatal("rejected fence escaped", rest, err)
	}
	if again, err := stream.Push("retry", true); err == nil || again != "" {
		t.Fatal("failed stream resumed")
	}
	unclosed := NewFenceStream(func(_, body string) (string, error) { return body, nil })
	if body, err := unclosed.Push("```forge-report\n{}", true); err == nil || body != "" {
		t.Fatal("unclosed fence escaped")
	}
}
