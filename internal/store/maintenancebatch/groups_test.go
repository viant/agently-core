package maintenancebatch

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestGroupsKeepsEveryGuardAndBoundsBatches(t *testing.T) {
	type guard struct{ owner, reference string }
	type row struct {
		guard guard
		id    int
	}
	rows := []row{{guard{"other", "ref"}, -1}, {guard{"owner", "different"}, -2}}
	for i := 0; i < Size+1; i++ {
		rows = append(rows, row{guard{"owner", "ref"}, i})
	}
	var keys []guard
	var lengths []int
	err := Groups(context.Background(), rows, func(r row) (guard, error) { return r.guard, nil }, func(k guard, batch []row) error {
		keys = append(keys, k)
		lengths = append(lengths, len(batch))
		for _, r := range batch {
			if r.guard != k {
				t.Fatal("crossed a writer guard")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(lengths, []int{1, 1, Size, 1}) || !reflect.DeepEqual(keys, []guard{{"other", "ref"}, {"owner", "different"}, {"owner", "ref"}, {"owner", "ref"}}) {
		t.Fatalf("keys=%v lengths=%v", keys, lengths)
	}
}

func TestGroupsValidatesBeforeMutationAndStopsOnFailure(t *testing.T) {
	failure := errors.New("failure")
	calls := 0
	apply := func(int, []int) error { calls++; return failure }
	err := Groups(context.Background(), []int{1, 2}, func(r int) (int, error) {
		if r == 2 {
			return 0, failure
		}
		return r, nil
	}, apply)
	if !errors.Is(err, failure) || calls != 0 {
		t.Fatalf("validation: err=%v calls=%d", err, calls)
	}
	err = Groups(context.Background(), []int{1, 2}, func(r int) (int, error) { return r, nil }, apply)
	if !errors.Is(err, failure) || calls != 1 {
		t.Fatalf("apply: err=%v calls=%d", err, calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = Groups(ctx, []int{1}, func(r int) (int, error) { return r, nil }, apply)
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("cancel: err=%v calls=%d", err, calls)
	}
}
