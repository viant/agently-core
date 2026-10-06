package manage

import (
	"fmt"
	"github.com/google/uuid"
	store "github.com/viant/agently-core/app/store/agui"
	nativeread "github.com/viant/agently-core/internal/datly/agui/thread/native/read"
	threadread "github.com/viant/agently-core/internal/datly/agui/thread/read"
	threadwrite "github.com/viant/agently-core/internal/datly/agui/thread/write"
)

func (t *operation) nativeIdentity(id string) (*nativeread.NativeIdentity, error) {
	input := &nativeread.Input{}
	input.SetNativeID(id)
	value, e := t.call("thread/native/read", "reader", "GET", input)
	if e != nil {
		return nil, e
	}
	out, ok := value.(*nativeread.Output)
	if !ok || out == nil {
		return nil, fmt.Errorf("native thread identity returned %T", value)
	}
	if len(out.Data) > 1 {
		return nil, store.ErrConflict
	}
	if len(out.Data) == 0 {
		return nil, nil
	}
	row := out.Data[0]
	// SQL collations may return a case/PAD alias. It is not the requested native ID.
	if row == nil || str(row.Id) != id {
		return nil, nil
	}
	if str(row.CreatedByUserId) != t.principal {
		return nil, store.ErrNotFound
	}
	return row, nil
}
func (t *operation) nativeThread(id string) (*threadread.Thread, error) {
	identity, e := t.nativeIdentity(id)
	if e != nil || identity == nil {
		return nil, e
	}
	input := &threadread.Input{}
	input.SetPrincipal(t.principal)
	input.SetKey(store.ThreadKey(id))
	input.SetNativeID(id)
	value, e := t.call("thread/read", "reader", "GET", input)
	if e != nil {
		return nil, e
	}
	out, ok := value.(*threadread.Output)
	if !ok || out == nil {
		return nil, fmt.Errorf("native thread returned %T", value)
	}
	if len(out.Data) != 1 || out.Data[0] == nil || str(out.Data[0].Id) != id {
		return nil, store.ErrNotFound
	}
	return out.Data[0], nil
}
func (t *operation) ensureThread() (*threadread.Thread, bool, error) {
	existing, e := t.thread()
	if e != nil {
		return nil, false, e
	}
	if existing != nil {
		return existing, false, nil
	}
	native, e := t.nativeThread(t.threadID)
	if e != nil {
		return nil, false, e
	}
	row := &threadwrite.Thread{}
	row.SetThreadId(ptr(t.threadID))
	row.SetThreadKey(ptr(store.ThreadKey(t.threadID)))
	row.SetPrincipal(ptr(t.principal))
	row.SetStateJson([]byte(`null`))
	row.SetMessagesJson([]byte(`[]`))
	mode := "create"
	if native != nil {
		if native.Revision != 0 || str(native.Principal) != "" || str(native.ThreadKey) != "" {
			return nil, false, store.ErrConflict
		}
		row.SetId(native.Id)
		row.SetRevision(0)
		mode = "bind"
	} else {
		row.SetId(ptr(uuid.NewString()))
		row.SetCreatedByUserId(ptr(t.principal))
		row.SetProtocolOnly(true)
		row.SetRevision(1)
	}
	if e = t.writeThread(row, mode, 1); e != nil {
		return nil, false, e
	}
	result, e := t.thread()
	if e != nil {
		return nil, false, e
	}
	if result == nil {
		return nil, false, store.ErrConflict
	}
	return result, true, nil
}
