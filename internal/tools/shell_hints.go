package tools

import (
	"fmt"
	"path/filepath"
	"strings"
)

func literalWord(word string, literal bool) string {
	if !literal {
		return ""
	}
	return word
}

func literalWordHint(word string) string {
	switch {
	case word == "":
		return ""
	case isRooted(word):
		return literalWordWords
	}
	return literalWordRelativeWords
}

const (
	literalWordWords         = "\nThat word is inside an inline program: write it another way, or save the program as a file and run that."
	literalWordRelativeWords = "\nThat word is inside an inline program: write it another way, give the call a cwd under your work folder, or save the program as a file and run that."
)

const holdsSubstrateWhy = "holds your home or a place the host keeps (or lies in one)"

func slashSearchHint(cmd, refused string) string {
	return searchHintFor(wordsHolding(cmd, refused), refused, spelledRoot(refused))
}

func searchHintFor(words []string, refused string, spelled bool) string {
	if !strings.HasPrefix(refused, "/") && !spelled {
		return ""
	}
	for _, w := range words {
		if searchPrograms[strings.TrimSuffix(filepath.Base(w), ".exe")] {
			if w == refused {

				return ""
			}
			return fmt.Sprintf("\nif that is search text and not a path, the grep tool takes any pattern: grep pattern=%q path=\"…\"", refused)
		}
	}
	return ""
}

func readFrom(home, base string) string {
	if base == home {
		return ""
	}
	return " (relative paths are read from the cwd you gave, " + base + ")"
}

func cdHint(cmd, refused, base string) string {
	if refused == "" || isRooted(refused) {
		return ""
	}
	for _, w := range wordsHolding(cmd, refused) {
		if w == refused {
			return ""
		}
		if cdWords[strings.ToLower(w)] {
			return "\nrelative paths are read from " + base + ", not from where a cd inside the command goes: give the shell cwd=<that directory> and write the path from there, or write the path in full"
		}
	}
	return ""
}

func notAPathHint(cmd, refused string) string {
	if slashSearchHint(cmd, refused) != "" || !textHintOffered(shellWordsQuoted(cmd, refused), refused, spelledRoot(refused)) {
		return ""
	}
	return "\nif that is text and not a path (a division, a string in a program, a JSON body), this check cannot tell: put the program or the text in a file with the write tool and name the file in the command"
}

func textHintOffered(words []shellWord, refused string, spelled bool) bool {
	if !strings.HasPrefix(refused, "/") && !spelled {
		return false
	}
	if strings.Trim(refused, "/") == "" {
		return true
	}
	for _, w := range words {
		if w.text == refused && !(spelled && w.quoted) {
			return false
		}
	}
	return true
}
