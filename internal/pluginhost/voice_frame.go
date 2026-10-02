package pluginhost

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

type VoiceFrame struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	Sequence  int64  `json:"sequence"`

	SynthesisID  string `json:"synthesis_id"`
	OutputStream *int64 `json:"output_stream"`

	Reason string `json:"reason"`

	Text    string `json:"text"`
	Speaker string `json:"speaker"`

	TrackID            string `json:"-"`
	TrackDeclared      bool   `json:"-"`
	StartSample        *int64 `json:"start_sample"`
	EndSample          *int64 `json:"end_sample"`
	StreamID           string `json:"stream_id"`
	ProcessedEndSample int64  `json:"processed_end_sample"`

	RefersTo         int64    `json:"refers_to"`
	SpeakerID        string   `json:"speaker_id"`
	Decision         string   `json:"decision"`
	Score            *float64 `json:"score"`
	Late             bool     `json:"late"`
	SpeakerUUID      string   `json:"speaker_uuid"`
	RegistryRevision string   `json:"registry_revision"`
	Continuity       string   `json:"continuity"`
	DisplayLabel     string   `json:"display_label"`
	Revision         uint64   `json:"revision"`

	Models struct {
		OperatorSettings map[string]interface{} `json:"operator_settings"`
	} `json:"models"`

	Unread []string `json:"-"`
}

func DecodeVoiceFrame(raw []byte) (VoiceFrame, error) {
	if t := bytes.TrimLeft(raw, " \t\r\n"); len(t) == 0 || t[0] != '{' {
		return VoiceFrame{}, &MalformedFrame{Reason: "the frame is not a JSON object"}
	}
	f, err := decodeWire(raw)
	if err == nil {
		return f, nil
	}

	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return f, malformed(raw, err)
	}
	first, unread, unreadNames := err, []string(nil), []string(nil)
	for err != nil {
		var te *json.UnmarshalTypeError
		if !errors.As(err, &te) || te.Field == "" {
			return f, malformed(raw, first)
		}
		name, _, _ := strings.Cut(te.Field, ".")
		removed := false
		for k := range fields {
			if strings.EqualFold(k, name) {
				delete(fields, k)
				removed = true
			}
		}
		if !removed {
			return f, malformed(raw, first)
		}
		unread, unreadNames = append(unread, reasonOf(te)), append(unreadNames, name)
		rest, merr := json.Marshal(fields)
		if merr != nil {
			return f, malformed(raw, first)
		}
		f, err = decodeWire(rest)
	}
	for _, name := range unreadNames {
		if !lenient(f.Type, name) {
			return f, malformed(raw, first)
		}
	}
	f.Unread = unread
	return f, nil
}

func lenient(typ, name string) bool {
	switch {
	case name == "type" || name == "session_id":
		return false
	case typ == EventTranscriptPartial || typ == EventTranscriptFinal || typ == EventSpeakerObservation:
		return false
	case typ == EventInputFinished:
		return false
	case typ == EventCancellation && name == "synthesis_id":
		return false
	}
	return true
}

func decodeWire(raw []byte) (VoiceFrame, error) {
	var wire struct {
		VoiceFrame
		Track json.RawMessage `json:"track_id"`
	}
	err := json.Unmarshal(raw, &wire)
	f := wire.VoiceFrame
	if wire.Track != nil {
		f.TrackDeclared = true
		if terr := json.Unmarshal(wire.Track, &f.TrackID); terr != nil && err == nil {
			var te *json.UnmarshalTypeError
			if errors.As(terr, &te) {
				te.Field = "track_id"
			}
			err = terr
		}
	}
	return f, err
}

type MalformedFrame struct {
	Type      string
	SessionID string
	Sequence  int64
	Sequenced bool
	Reason    string
}

func (m *MalformedFrame) Error() string { return m.Reason }

func reasonOf(te *json.UnmarshalTypeError) string {
	return fmt.Sprintf("%s is %s; the speech interface gives %s", te.Field, te.Value, kindOf(te.Type))
}

func kindOf(t reflect.Type) string {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case t == nil:
		return "another type"
	case t.Kind() == reflect.String:
		return "a string"
	case t.Kind() == reflect.Bool:
		return "true or false"
	case t.Kind() >= reflect.Int && t.Kind() <= reflect.Int64:
		return "an integer"
	case t.Kind() >= reflect.Uint && t.Kind() <= reflect.Uint64:
		return "a non-negative integer"
	case t.Kind() == reflect.Float32 || t.Kind() == reflect.Float64:
		return "a number"
	default:
		return "an object"
	}
}

func malformed(raw []byte, err error) *MalformedFrame {
	m := &MalformedFrame{Reason: "the frame is not JSON: " + err.Error()}
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) {
		m.Reason = reasonOf(te)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return m
	}
	if json.Unmarshal(fields["type"], &m.Type) != nil {
		m.Type = ""
	}
	if json.Unmarshal(fields["session_id"], &m.SessionID) != nil {
		m.SessionID = ""
	}
	if s, ok := fields["sequence"]; ok {
		m.Sequenced = json.Unmarshal(s, &m.Sequence) == nil
		if !m.Sequenced {
			m.Sequence = 0
		}
	}
	return m
}
