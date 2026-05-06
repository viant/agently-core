package skill

import "strings"

const (
	ActivationNameKey     = "skillActivationName"
	ActivationBodyKey     = "skillActivationBody"
	ActivationModeKey     = "skillActivationMode"
	ActivationArgsKey     = "skillActivationArgs"
	ActivationEmbeddedKey = "skillActivationEmbedded"
)

type ActivationContext struct {
	Name     string
	Body     string
	Mode     string
	Args     string
	Embedded bool
}

func BuildActivationContext(value ActivationContext) map[string]interface{} {
	name := strings.TrimSpace(value.Name)
	body := strings.TrimSpace(value.Body)
	if name == "" || body == "" {
		return nil
	}
	mode := strings.TrimSpace(value.Mode)
	if mode == "" {
		mode = "inline"
	}
	return map[string]interface{}{
		ActivationNameKey:     name,
		ActivationBodyKey:     body,
		ActivationModeKey:     mode,
		ActivationArgsKey:     strings.TrimSpace(value.Args),
		ActivationEmbeddedKey: value.Embedded,
	}
}

func ReadActivationContext(values map[string]interface{}) (ActivationContext, bool) {
	if len(values) == 0 {
		return ActivationContext{}, false
	}
	name, _ := values[ActivationNameKey].(string)
	body, _ := values[ActivationBodyKey].(string)
	mode, _ := values[ActivationModeKey].(string)
	args, _ := values[ActivationArgsKey].(string)
	name = strings.TrimSpace(name)
	body = strings.TrimSpace(body)
	mode = strings.TrimSpace(mode)
	args = strings.TrimSpace(args)
	if name == "" || body == "" {
		return ActivationContext{}, false
	}
	embedded := false
	switch value := values[ActivationEmbeddedKey].(type) {
	case bool:
		embedded = value
	case string:
		embedded = strings.EqualFold(strings.TrimSpace(value), "true")
	}
	return ActivationContext{
		Name:     name,
		Body:     body,
		Mode:     mode,
		Args:     args,
		Embedded: embedded,
	}, true
}
