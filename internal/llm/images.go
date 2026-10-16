package llm

func WritesImages(d Dialect) bool {
	return d != DialectGemini
}
