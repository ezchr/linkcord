package json

import (
	"bytes"
	jsonv1 "encoding/json"
	"reflect"
	"testing"
)

type nullValue struct{}

func (*nullValue) MarshalJSON() ([]byte, error) { return []byte("null"), nil }

type compatibilityValue struct {
	Bool   bool              `json:"bool,omitempty"`
	Slice  []string          `json:"slice"`
	Map    map[string]string `json:"map"`
	Bytes  [2]byte           `json:"bytes"`
	Custom *nullValue        `json:"custom,omitempty"`
}

func TestDefaultDriverMarshalCompatibility(t *testing.T) {
	for _, value := range []compatibilityValue{
		{},
		{Slice: []string{}, Map: map[string]string{}, Custom: &nullValue{}},
	} {
		want, err := jsonv1.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("got %s, want %s", got, want)
		}
	}
}

func TestDefaultDriverUnmarshalCompatibility(t *testing.T) {
	type value struct {
		Name string
	}
	input := []byte(`{"name":"first","Name":"second"}`)

	var got, want value
	if err := jsonv1.Unmarshal(input, &want); err != nil {
		t.Fatal(err)
	}
	if err := Unmarshal(input, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestDefaultDriverStreamCompatibility(t *testing.T) {
	value := compatibilityValue{}

	var gotEncoded, wantEncoded bytes.Buffer
	if err := EncodeStream(&gotEncoded, value); err != nil {
		t.Fatal(err)
	}
	if err := jsonv1.NewEncoder(&wantEncoded).Encode(value); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotEncoded.Bytes(), wantEncoded.Bytes()) {
		t.Fatalf("got %q, want %q", gotEncoded.Bytes(), wantEncoded.Bytes())
	}

	input := []byte(`{"Value":1} {"Value":2}`)
	var got, want struct{ Value int }
	if err := DecodeStream(bytes.NewReader(input), &got); err != nil {
		t.Fatal(err)
	}
	if err := jsonv1.NewDecoder(bytes.NewReader(input)).Decode(&want); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
