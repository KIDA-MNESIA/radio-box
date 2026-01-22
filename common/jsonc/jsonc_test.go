package jsonc_test

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/common/jsonc"
)

type sample struct {
	A int    `json:"a"`
	B string `json:"b"`
}

func TestUnmarshalExtended_JSONC(t *testing.T) {
	input := []byte(`{
		// line comment
		"a": 1,
		"b": "x", /* block comment */
	}`)
	v, err := jsonc.UnmarshalExtended[sample](input)
	if err != nil {
		t.Fatalf("UnmarshalExtended failed: %v", err)
	}
	if v.A != 1 || v.B != "x" {
		t.Fatalf("unexpected value: %+v", v)
	}
}

func TestUnmarshalExtendedContext_JSONC(t *testing.T) {
	input := []byte(`{
		"a": 2, // trailing comment
		"b": "y",
	}`)
	v, err := jsonc.UnmarshalExtendedContext[sample](context.Background(), input)
	if err != nil {
		t.Fatalf("UnmarshalExtendedContext failed: %v", err)
	}
	if v.A != 2 || v.B != "y" {
		t.Fatalf("unexpected value: %+v", v)
	}
}
