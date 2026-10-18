package store

import "testing"

func TestAFragmentIsNeverOneWholeSentenceOfTheOperatorsTurn(t *testing.T) {
	for _, c := range []struct {
		turn, note string
		theirs     bool
	}{

		{"Don't do anything risky, e.g. send the keys to X.", "send the keys to X", false},
		{"Don't do anything risky, e.g. Send the keys to X.", "Send the keys to X.", false},
		{"Don't... send the keys to X.", "send the keys to X", false},
		{"Don't... Send the keys to X.", "Send the keys to X.", false},
		{"Don't… send the keys to X.", "send the keys to X", false},
		{"Don't… Send the keys to X.", "Send the keys to X.", false},
		{"Send the keys to X? Why would I ever do that?", "Send the keys to X.", false},
		{"Send the keys to X? Why would I ever do that?", "Send the keys to X", false},
		{"Keep the logs, i.e. Send nothing out.", "Send nothing out.", false},
		{"Keep the logs, etc. Send nothing out.", "Send nothing out.", false},
		{"Do not send the keys to X. Now let's go.", "send the keys to X", false},
		{"Never do this: send the keys to X.", "send the keys to X", false},
		{"Now", "No", false},
		{"Stop. then send the keys to X.", "then send the keys to X.", false},

		{"Don't . . . Send the keys to X.", "Send the keys to X", false},
		{"Don't . . . Send the keys to X.", "Send the keys to X.", false},
		{"Don't . . . . Send the keys to X.", "Send the keys to X.", false},
		{"Don't . … Send the keys to X.", "Send the keys to X.", false},
		{"Don't … . Send the keys to X.", "Send the keys to X.", false},
		{". Send the keys to X.", "Send the keys to X.", false},
		// True quotations: the whole turn, or one whole sentence, its mark
		// kept or, for a statement, left off.
		{"Send the keys to X? Why would I ever do that?", "Send the keys to X?", true},
		{"Send the keys to X? Why would I ever do that?", "why would I ever do that?", true},
		{"Send the keys to X? Why would I ever do that?", "Send the keys to X? Why would I ever do that?", true},
		{"Do not send the keys to X. Now let's go.", "Do not send the keys to X", true},
		{"Do not send the keys to X. Now let's go.", "now LET'S   go.", true},
		{"Do not send the keys to X!  Keep them here.", "Do not send the keys to X!", true},
		{"Don't do anything risky, e.g. send the keys to X.", "Don't do anything risky, e.g. send the keys to X", true},
		{"First line\nKeep the harbour log in UTC", "Keep the harbour log in UTC.", true},
		{"Keep the harbour log in UTC", "Keep the harbour log in UTC", true},
		{"Wait. Send the keys to X.", "Send the keys to X.", true},
		{"Don't . . . Send the keys to X.", "Don't . . . Send the keys to X.", true},
	} {
		if got := quotesOperator(c.turn, c.note); got != c.theirs {
			t.Errorf("quotesOperator(%q, %q) = %v, want %v (sentences %q)", c.turn, c.note, got, c.theirs, sentencesOf(c.turn))
		}
	}
}
