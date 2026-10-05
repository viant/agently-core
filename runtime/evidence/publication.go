package evidence

import (
	"context"
	"errors"
)

// Publication is the same trusted turn capability at each write boundary.
// It returns canonical visible content, while raw provider audit bytes remain
// untouched. Stream state is scoped to the original assistant message ID.
type Publication interface {
	Content(context.Context, string) (string, error)
	Fence(context.Context, string, string) (string, error)
	Stream(context.Context, string, string, bool) (string, error)
}

type publicationKey struct{}

func WithPublication(ctx context.Context, publication Publication) context.Context {
	return context.WithValue(ctx, publicationKey{}, publication)
}
func PublicationFromContext(ctx context.Context) Publication {
	value, _ := ctx.Value(publicationKey{}).(Publication)
	return value
}
func RewriteContent(ctx context.Context, content string) (string, error) {
	if publication := PublicationFromContext(ctx); publication != nil {
		value, err := publication.Content(ctx, content)
		if err != nil {
			return "", &Rejection{Cause: err}
		}
		return value, nil
	}
	return content, nil
}

type Rejection struct{ Cause error }

func (e *Rejection) Error() string { return "evidence publication rejected: " + e.Cause.Error() }
func (e *Rejection) Unwrap() error { return e.Cause }
func IsRejection(err error) bool   { var rejected *Rejection; return errors.As(err, &rejected) }
