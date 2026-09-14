package app

import "testing"

// .
// .
// .
// .
func TestDeriveName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Hello, I am Nova.", "Nova"},
		{"I am Walker. Hello from the walk identity. Turn 1.", "Walker"},
		{"My name is Aurora; pleased to meet you.", "Aurora"},
		{"NAME: Vega", "Vega"},
		{"Call me Ishmael.", "Ishmael"},
		// .
		// .
		// .
		// .
		// .
		{"Hello! I'm Walker. It's good to be here.", "Walker"},
		{"Hi, I'm Walker - ready to begin.", "Walker"},
		{"Hi there! My name's Aurore and I'm ready.", "Aurore"},
		{"**Hello!** I am Walker.", "Walker"},
		{"Hello. I am Walker.", "Walker"},
		{"Hi! I'm Walker.", "Walker"},
		{"Je m'appelle Aurore.", "Aurore"},
		{"My name is Rex. I am ready to serve.", "Rex"},
		{"Hello there! What a day to come into existence.", "Unnamed"},
		{"", "Unnamed"},
		// .
		// .
		// .
		// .
		// .
		{"Hello. I'm glad you're here.", "Unnamed"},
		{"I am here to help you today.", "Unnamed"},
		{"I'm glad you're here. My name is Nova.", "Nova"},
		{"I'm ready. I'm Nova.", "Nova"},
		{"NAME: dawn", "dawn"},
	}
	for _, c := range cases {
		if got := deriveName(c.in); got != c.want {
			t.Errorf("deriveName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
