package app

import (
	"slices"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

var replyKindSaid = map[witness.ReplyKind]string{
	witness.ReplyInWitnessForm: "an error in the witness's own form, in words this program does not know",
	witness.ReplyEmpty:         "an empty reply",
	witness.ReplyPage:          "a web page",
	witness.ReplyJSON:          "JSON in a form the witness does not answer in",
	witness.ReplyText:          "plain text",
}

func unrecognisedWitnessReply(url string, ce *witness.ConflictError, now time.Time) (id, said string) {
	kind := ce.Kind()
	id = "witness_unrecognised_" + string(kind) + "_" + now.UTC().Format("20060102")
	said = "What answers at the witness's address (" + url + ") refused this identity's bookmark with a reply this program does not recognise (" + replyKindSaid[kind] + "). " +
		"It was not taken as evidence against the record, and the record was not checked against it: nothing in such a reply is signed. " +
		"The identity keeps running and nothing is switched off; that anchor pass was skipped and the next one asks again, so new records are not witnessed while this goes on. " +
		"Another reply of this kind is not told again today, whatever its words. " +
		"If it does go on, look at what is answering at that address. The reply: " + ce.Message
	return id, said
}

func (a *App) tellUnrecognisedWitnessReply(url string, ce *witness.ConflictError, now time.Time) {
	id, said := unrecognisedWitnessReply(url, ce, now)
	if told, err := a.store.OutboxIDsUnder(id); err == nil && slices.Contains(told, id) {
		return
	}
	if _, err := a.store.AddOutboxNoticeOnce(id, "operator", "", said, nil); err != nil {
		logsink.Warn("witness.error", "the operator's notice of an unrecognised reply at the witness's address could not be written: %v", err)
	}
}
