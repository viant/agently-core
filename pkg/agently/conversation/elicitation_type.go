package conversation

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/viant/xdatly/types/core"
	"github.com/viant/xdatly/types/custom/dependency/checksum"
)

// Elicitation holds hydrated elicitation payload data attached to a message.
type Elicitation map[string]interface{}

func (e *Elicitation) Scan(src interface{}) error {
	if e == nil {
		return nil
	}
	if src == nil {
		*e = nil
		return nil
	}
	var raw string
	switch actual := src.(type) {
	case string:
		raw = actual
	case []byte:
		raw = string(actual)
	default:
		return fmt.Errorf("unsupported elicitation scan type %T", src)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		*e = nil
		return nil
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return err
	}
	*e = Elicitation(out)
	return nil
}

func (e Elicitation) Value() (driver.Value, error) {
	if len(e) == 0 {
		return nil, nil
	}
	data, err := json.Marshal(map[string]interface{}(e))
	if err != nil {
		return nil, err
	}
	return string(data), nil
}

func init() {
	core.RegisterType("conversation", "Elicitation", reflect.TypeOf(Elicitation{}), checksum.GeneratedTime)
}
