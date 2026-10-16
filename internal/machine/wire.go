package machine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

const (
	MsgHello = "hello"

	MsgHandshake = "handshake"

	MsgStatus = "status"

	MsgUpdateForce = "update.force"

	MsgNeed = "need"

	MsgRelease = "release"

	MsgStorePlace = "store.place"

	MsgForget = "forget"
)

const FrameJSON = "json"

type Speaks struct {
	Messages []string `json:"messages"`
	Frames   []string `json:"frames"`
}

func speaks() Speaks {
	return Speaks{Messages: []string{MsgHello, MsgHandshake, MsgStatus, MsgUpdateForce, MsgNeed, MsgRelease, MsgStorePlace, MsgForget}, Frames: []string{FrameJSON}}
}

func (s Speaks) has(message string) bool {
	for _, m := range s.Messages {
		if m == message {
			return true
		}
	}
	return false
}

const maxFrame = bbb.MaxControlFrameBytes

const linkPrefix = "AII-ENGINE-LINK"

func linkInput(instance, nonce, identity string) []byte {
	return []byte(linkPrefix + "\nservice:" + instance + "\nnonce:" + nonce + "\nidentity:" + identity + "\n")
}

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int      `json:"code"`
	Message string   `json:"message"`
	Data    *Refusal `json:"data,omitempty"`
}

type Refusal struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
	Remedy  string `json:"remedy,omitempty"`
}

func (r *Refusal) Error() string {
	if r.Remedy != "" {
		return r.Message + " — " + r.Remedy
	}
	return r.Message
}

const (
	ReasonBadRequest    = "bad_request"
	ReasonReplayedNonce = "replayed_nonce"
	ReasonBadSignature  = "bad_signature"
	ReasonWrongKey      = "wrong_key"
	ReasonUnregistered  = "unregistered_slot"
	ReasonNotRecorded   = "not_recorded"
	ReasonNotSpoken     = "not_spoken"
	ReasonNoUpdate      = "no_update"
	ReasonHandingOver   = "handing_over"
	ReasonNoStore       = "no_store"
	ReasonStorePlace    = "store_place"
)

const (
	codeInvalidParams  = -32602
	codeMethodNotFound = -32601
	codeRefused        = -32000
)

type helloParams struct {
	Service string `json:"service"`
	Nonce   string `json:"nonce"`
	Speaks  Speaks `json:"speaks"`
	Build   Build  `json:"build"`
}

type handshakeParams struct {
	Identity  string `json:"identity"`
	PublicKey string `json:"public_key"`
	Slot      string `json:"slot"`
	Nonce     string `json:"nonce"`
	Signature string `json:"signature"`
	Speaks    Speaks `json:"speaks"`
	Build     Build  `json:"build"`

	Register bool `json:"register,omitempty"`
}

type handshakeResult struct {
	Identity string `json:"identity"`
	Recorded string `json:"recorded"`
}

type Status struct {
	Service  string  `json:"service"`
	Build    Build   `json:"build"`
	Speaks   Speaks  `json:"speaks"`
	Identity string  `json:"identity"`
	Linked   int     `json:"linked"`
	Update   *Update `json:"update,omitempty"`

	Store string    `json:"store,omitempty"`
	Refs  []LinkRef `json:"refs,omitempty"`
}

type LinkRef struct {
	Plugin string `json:"plugin"`
	Entry  string `json:"entry"`
}

type NeedParams struct {
	Plugin  string                 `json:"plugin"`
	Works   []pluginhost.StoreWork `json:"works"`
	Adopt   pluginhost.Adoption    `json:"adopt"`
	Damaged []string               `json:"damaged,omitempty"`
	Retry   bool                   `json:"retry,omitempty"`
}

const (
	NeedPresent = "present"
	NeedPending = "pending"
	NeedWaiting = "waiting"
	NeedEnded   = "ended"
	NeedRefused = "refused"
)

type NeedResult struct {
	Store   string `json:"store"`
	State   string `json:"state"`
	Cause   string `json:"cause,omitempty"`
	Remedy  string `json:"remedy,omitempty"`
	AfterMS int64  `json:"after_ms,omitempty"`
}

type ReleaseParams struct {
	Refs []LinkRef `json:"refs"`
}

type PlaceParams struct {
	Dir string `json:"dir"`
}

type PlaceResult struct {
	Store    string   `json:"store"`
	Draining []string `json:"draining,omitempty"`
}

type Update struct {
	Build Build  `json:"build"`
	Since string `json:"since"`
}

func writeMessage(w io.Writer, m message) error {
	m.JSONRPC = "2.0"
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return bbb.WriteFrame(w, raw, maxFrame)
}

func readMessage(r io.Reader) (message, error) {
	raw, err := bbb.ReadFrame(r, maxFrame)
	if err != nil {
		return message{}, err
	}
	var m message
	if err := json.Unmarshal(raw, &m); err != nil {
		return message{}, fmt.Errorf("a frame that is not JSON-RPC: %w", err)
	}
	if m.JSONRPC != "2.0" {
		return message{}, fmt.Errorf("a frame that is not JSON-RPC 2.0")
	}
	return m, nil
}

func decodeStrict(raw json.RawMessage, v any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage("{}")
	}
	return canonicaljson.DecodeStrict(raw, v)
}

func mustJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {

		panic("machine: encode a result: " + err.Error())
	}
	return raw
}
