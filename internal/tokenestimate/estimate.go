package tokenestimate

import (
	"encoding/json"
	"fmt"
)

func Estimate(text string) int {
	return (len(text) + 2) / 3
}

func EstimateWire(text string) int {
	b, err := json.Marshal(text)
	if err != nil {
		return Estimate(text)
	}
	return Estimate(string(b))
}

func Stated(maxTokens int) string {
	return fmt.Sprintf(statedText, maxTokens*3*85/100, maxTokens)
}

const statedText = "Aim for about %d characters. A character of plain English counts one; a line break, a straight double quote or a backslash two; a long dash, a curly quote or an accented letter two or three; and &, < or > six. Over %d tokens, at three of those counts to a token, it is refused."
