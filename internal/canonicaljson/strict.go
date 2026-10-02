package canonicaljson

import (
	"bytes"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"reflect"
)

var ErrTrailingContent = errors.New("content after the JSON value")

func DecodeStrict(raw []byte, out any) error { return decodeStrict(raw, out, false) }

func DecodeStrictNumbers(raw []byte, out any) error { return decodeStrict(raw, out, true) }

func decodeStrict(raw []byte, out any, numbers bool) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if numbers {
		dec.UseNumber()
	}
	if err := dec.Decode(out); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return ErrTrailingContent
	}
	return exactNames(raw, out)
}

func exactNames(raw []byte, out any) error {
	fresh := reflect.New(reflect.TypeOf(out).Elem()).Interface()
	if err := jsonv2.Unmarshal(raw, fresh, jsonv2.RejectUnknownMembers(true)); err != nil {
		return fmt.Errorf("a member name must match its field exactly and appear once: %w", err)
	}
	return nil
}
