package plugstore

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const RecordFormat = 1

type RecordError struct {
	Path    string
	Missing []string
	Detail  string
}

func (e *RecordError) Error() string {
	where := e.Path
	if where == "" {
		where = "a store record"
	}
	if len(e.Missing) > 0 {
		return fmt.Sprintf("%s lacks %s, which this build reads; it is left as it is", where, strings.Join(e.Missing, ", "))
	}
	return fmt.Sprintf("%s does not read: %s; it is left as it is", where, e.Detail)
}

type RootRecord struct {
	VariantID        string
	InventorySHA256  string
	ArchiveSHA256    string
	Entrypoint       string
	EntrypointSHA256 string
	Files            int
	InstalledBytes   int64
	Inventory        []byte
	InstalledAt      string

	other map[string]json.RawMessage
}

type ViewRecord struct {
	ModelsSHA256 string
	Names        []string
	CreatedAt    string

	other map[string]json.RawMessage
}

var (
	rootRequired = []string{"format", "variant_id", "inventory_sha256", "archive_sha256", "entrypoint", "entrypoint_sha256", "files", "installed_bytes", "inventory"}
	viewRequired = []string{"format", "models_sha256", "names"}
)

func encodeRecord(known map[string]any, other map[string]json.RawMessage) ([]byte, error) {
	out := make(map[string]any, len(known)+len(other)+1)
	for k, v := range other {
		out[k] = v
	}
	for k, v := range known {
		out[k] = v
	}
	out["format"] = RecordFormat
	return json.MarshalIndent(out, "", "  ")
}

func (r RootRecord) MarshalJSON() ([]byte, error) {
	inv := r.Inventory
	if inv == nil {
		inv = []byte{}
	}
	return encodeRecord(map[string]any{
		"variant_id": r.VariantID, "inventory_sha256": r.InventorySHA256, "archive_sha256": r.ArchiveSHA256,
		"entrypoint": r.Entrypoint, "entrypoint_sha256": r.EntrypointSHA256, "files": r.Files,
		"installed_bytes": r.InstalledBytes, "inventory": inv, "installed_at": r.InstalledAt,
	}, r.other)
}

func (r ViewRecord) MarshalJSON() ([]byte, error) {
	names := append([]string{}, r.Names...)
	sort.Strings(names)
	return encodeRecord(map[string]any{
		"models_sha256": r.ModelsSHA256, "names": names, "created_at": r.CreatedAt,
	}, r.other)
}

func decodeMembers(path string, raw []byte, required []string) (map[string]json.RawMessage, error) {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, &RecordError{Path: path, Detail: err.Error()}
	}
	var missing []string
	for _, k := range required {
		if v, ok := all[k]; !ok || string(v) == "null" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return nil, &RecordError{Path: path, Missing: missing}
	}
	var format int
	if err := json.Unmarshal(all["format"], &format); err != nil {
		return nil, &RecordError{Path: path, Detail: "format: " + err.Error()}
	}
	if format != RecordFormat {
		return nil, &RecordError{Path: path, Detail: fmt.Sprintf("it is format %d, and this build reads format %d", format, RecordFormat)}
	}
	return all, nil
}

func member(path string, all map[string]json.RawMessage, key string, v any) error {
	raw, ok := all[key]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return &RecordError{Path: path, Detail: key + ": " + err.Error()}
	}
	return nil
}

func kept(all map[string]json.RawMessage, known ...string) map[string]json.RawMessage {
	for _, k := range append(known, "format") {
		delete(all, k)
	}
	if len(all) == 0 {
		return nil
	}
	return all
}

func DecodeRootRecord(path string, raw []byte) (RootRecord, error) {
	all, err := decodeMembers(path, raw, rootRequired)
	if err != nil {
		return RootRecord{}, err
	}
	var r RootRecord
	for _, m := range []struct {
		key string
		v   any
	}{
		{"variant_id", &r.VariantID}, {"inventory_sha256", &r.InventorySHA256}, {"archive_sha256", &r.ArchiveSHA256},
		{"entrypoint", &r.Entrypoint}, {"entrypoint_sha256", &r.EntrypointSHA256}, {"files", &r.Files},
		{"installed_bytes", &r.InstalledBytes}, {"inventory", &r.Inventory}, {"installed_at", &r.InstalledAt},
	} {
		if err := member(path, all, m.key, m.v); err != nil {
			return RootRecord{}, err
		}
	}
	if !ValidDigest(r.ArchiveSHA256) || !ValidDigest(r.InventorySHA256) {
		return RootRecord{}, &RecordError{Path: path, Detail: "its archive or inventory digest is not a sha256 digest"}
	}
	r.other = kept(all, "variant_id", "inventory_sha256", "archive_sha256", "entrypoint", "entrypoint_sha256", "files", "installed_bytes", "inventory", "installed_at")
	return r, nil
}

func DecodeViewRecord(path string, raw []byte) (ViewRecord, error) {
	all, err := decodeMembers(path, raw, viewRequired)
	if err != nil {
		return ViewRecord{}, err
	}
	var r ViewRecord
	for _, m := range []struct {
		key string
		v   any
	}{{"models_sha256", &r.ModelsSHA256}, {"names", &r.Names}, {"created_at", &r.CreatedAt}} {
		if err := member(path, all, m.key, m.v); err != nil {
			return ViewRecord{}, err
		}
	}
	r.other = kept(all, "models_sha256", "names", "created_at")
	return r, nil
}

func (r RootRecord) Other() map[string]json.RawMessage { return r.other }
func (r ViewRecord) Other() map[string]json.RawMessage { return r.other }

const maxRecordBytes = 64 << 20
