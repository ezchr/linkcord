// Package json allows for different implementations of JSON serializing, as
// well as extra optional types needed.
package json

import (
	jsonv1 "encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"io"
)

var v1Options = jsonv1.DefaultOptionsV1()

type Driver interface {
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error

	DecodeStream(r io.Reader, v any) error
	EncodeStream(w io.Writer, v any) error
}

type DefaultDriver struct{}

func (d DefaultDriver) Marshal(v any) ([]byte, error) {
	return jsonv2.Marshal(v, v1Options)
}

func (d DefaultDriver) Unmarshal(data []byte, v any) error {
	return jsonv2.Unmarshal(data, v, v1Options)
}

func (d DefaultDriver) DecodeStream(r io.Reader, v any) error {
	return jsonv2.UnmarshalDecode(jsontext.NewDecoder(r), v, v1Options)
}

func (d DefaultDriver) EncodeStream(w io.Writer, v any) error {
	return jsonv2.MarshalEncode(jsontext.NewEncoder(w), v, v1Options)
}

// Default is the default JSON driver, which uses encoding/json/v2.
var Default Driver = DefaultDriver{}

// Marshal uses the default driver.
func Marshal(v any) ([]byte, error) {
	return Default.Marshal(v)
}

// Unmarshal uses the default driver.
func Unmarshal(data []byte, v any) error {
	return Default.Unmarshal(data, v)
}

// DecodeStream uses the default driver.
func DecodeStream(r io.Reader, v any) error {
	return Default.DecodeStream(r, v)
}

// EncodeStream uses the default driver.
func EncodeStream(w io.Writer, v any) error {
	return Default.EncodeStream(w, v)
}
