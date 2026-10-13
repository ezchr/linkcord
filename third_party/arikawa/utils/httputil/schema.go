package httputil

import (
	"net/url"
	"sync"

	"github.com/gorilla/schema"
)

// SchemaEncoder expects the encoder to read the "schema" tags.
type SchemaEncoder interface {
	Encode(src any) (url.Values, error)
}

type DefaultSchema struct {
	*schema.Encoder
}

var _ SchemaEncoder = (*DefaultSchema)(nil)

var defaultSchemaEncoder = sync.OnceValue(schema.NewEncoder)

func (d *DefaultSchema) Encode(src any) (url.Values, error) {
	encoder := d.Encoder
	if encoder == nil {
		encoder = defaultSchemaEncoder()
	}

	v := url.Values{}
	return v, encoder.Encode(src, v)
}
