package jsonc

import (
	"context"

	"github.com/sagernet/sing/common/json"
	"github.com/tailscale/hujson"
)

// UnmarshalExtendedContext behaves like json.UnmarshalExtendedContext, but also
// accepts JSONC (comments + trailing commas) by standardizing input on-demand.
func UnmarshalExtendedContext[T any](ctx context.Context, b []byte) (T, error) {
	v, err := json.UnmarshalExtendedContext[T](ctx, b)
	if err == nil {
		return v, nil
	}
	b2, err2 := hujson.Standardize(b)
	if err2 != nil {
		var zero T
		return zero, err
	}
	return json.UnmarshalExtendedContext[T](ctx, b2)
}

// UnmarshalExtended behaves like json.UnmarshalExtended, but also accepts JSONC
// (comments + trailing commas) by standardizing input on-demand.
func UnmarshalExtended[T any](b []byte) (T, error) {
	v, err := json.UnmarshalExtended[T](b)
	if err == nil {
		return v, nil
	}
	b2, err2 := hujson.Standardize(b)
	if err2 != nil {
		var zero T
		return zero, err
	}
	return json.UnmarshalExtended[T](b2)
}
