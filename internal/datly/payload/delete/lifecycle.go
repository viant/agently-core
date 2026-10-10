package delete

import (
	"context"
	"fmt"
	"strings"
)

// MaxBatchSize bounds the private cleanup contract, not ordinary payload writes.
const MaxBatchSize = 400

// Init restricts this private key-only writer to explicitly marked deletions
// authorized by the trusted payloadaccess provider. Ordinary payload mutations
// still use payload/write. Reference predicates are also enforced by DELETE,
// so a reference arriving after the pre-read aborts the caller's transaction.
func (input *Input) Init(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !input.DeleteUnreferenced {
		return fmt.Errorf("payload delete requires trusted deleteUnreferenced access")
	}
	if len(input.Payloads) == 0 || len(input.Payloads) > MaxBatchSize {
		return fmt.Errorf("payload delete requires between 1 and %d payloads", MaxBatchSize)
	}
	// Validate the entire request before filtering missing keys or mutating rows.
	seen := make(map[string]bool, len(input.Payloads))
	for _, row := range input.Payloads {
		if row == nil || row.Has == nil || !row.Has.Id || strings.TrimSpace(row.Id) == "" || !row.Has.ShouldDelete || !row.ShouldDelete {
			return fmt.Errorf("payload delete requires marked identity and deletion")
		}
		if seen[row.Id] {
			return fmt.Errorf("payload delete requires distinct identities")
		}
		seen[row.Id] = true
	}
	// Init runs after input binding, including the unfiltered key-only current
	// read. An absent key is already gone: do not enqueue a mutation. Datly's
	// onDeleteNotFound=ignore deliberately stays strict with an active mutation
	// predicate. Do not catch a zero-row guarded DELETE later: that can mean a
	// new reference won the race and must roll back the caller's transaction.
	existing := make(map[string]bool, len(input.CurrentWriter))
	for _, row := range input.CurrentWriter {
		if row != nil {
			existing[row.Id] = true
		}
	}
	selected := make([]*PayloadDelete, 0, len(input.Payloads))
	for _, row := range input.Payloads {
		if existing[row.Id] {
			selected = append(selected, row)
		}
	}
	input.SetPayloads(selected)
	return nil
}
