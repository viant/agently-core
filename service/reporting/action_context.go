package reporting

import "context"

type createKindKey struct{}

func withReportCreateKind(ctx context.Context, kind string) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, createKindKey{}, kind)
}

// ReportCreateKind identifies the artifact kind assigned by the reporting
// service before its create action check. Callers cannot set this value through
// a request field; hosts use it only to select a configured collection policy.
func ReportCreateKind(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(createKindKey{}).(string)
	return value
}
