package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

func DecodeJSON(body []byte) (any, error) {
	var value any
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	if err := d.Decode(&value); err != nil {
		return nil, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON data")
	}
	return value, nil
}
