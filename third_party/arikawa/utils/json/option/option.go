// Package option provides generic optional and nullable values.
package option

import (
	"bytes"

	"github.com/ayn2op/arikawa/v3/utils/json"
)

// Option wraps a type to make it omittable.
type Option[T any] *T

// Some creates an Option containing v.
func Some[T any](v T) Option[T] {
	return &v
}

// PtrTo returns a pointer to v.
//
//go:fix inline
func PtrTo[T any](v T) *T {
	return new(v)
}

// Nullable distinguishes omitted, null, and non-null values.
type Nullable[T any] struct {
	Val  T
	Init bool
}

// SomeNullable creates a non-null Nullable.
func SomeNullable[T any](v T) *Nullable[T] {
	return &Nullable[T]{Val: v, Init: true}
}

func (v Nullable[T]) MarshalJSON() ([]byte, error) {
	if !v.Init {
		return []byte("null"), nil
	}
	return json.Marshal(v.Val)
}

func (v *Nullable[T]) UnmarshalJSON(b []byte) error {
	if bytes.Equal(b, []byte("null")) {
		*v = Nullable[T]{}
		return nil
	}
	v.Init = true
	return json.Unmarshal(b, &v.Val)
}
