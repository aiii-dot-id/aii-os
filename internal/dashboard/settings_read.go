package dashboard

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
)

type SettingRead struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Set   bool   `json:"set,omitempty"`
}

func ReadSettings(prefix string, block any, inForce map[string]string) []SettingRead {
	v := reflect.ValueOf(block)
	t := v.Type()
	rows := make([]SettingRead, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		f := v.Field(i)
		row := SettingRead{Key: prefix + "." + name, Set: !f.IsZero()}
		if said, ok := inForce[row.Key]; ok {
			row.Value = said
		} else if row.Set {
			row.Value = storedValue(f)
		} else {
			row.Value = "not set"
		}
		rows = append(rows, row)
	}
	return rows
}

func storedValue(f reflect.Value) string {
	for f.Kind() == reflect.Pointer {
		f = f.Elem()
	}
	switch f.Kind() {
	case reflect.Bool:
		return OnOff(f.Bool())
	case reflect.Int, reflect.Int64:
		return strconv.FormatInt(f.Int(), 10)
	case reflect.String:
		return f.String()
	}
	raw, err := json.Marshal(f.Interface())
	if err != nil {
		return "set"
	}
	return string(raw)
}

func OnOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}
