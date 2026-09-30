package tokenestimate

import "encoding/json"

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
