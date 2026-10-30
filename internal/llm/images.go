package llm

import (
	"fmt"
	"strings"
)

func Writes(d Dialect, mediaType string) bool {
	switch {
	case mediaType == "image/png" || mediaType == "image/jpeg" || mediaType == "image/webp":
		return true
	case mediaType == "image/gif":
		return d != DialectGemini
	case mediaType == "application/pdf":
		return true
	case mediaType == "audio/wav" || mediaType == "audio/mpeg":
		return d == DialectOpenAI || d == DialectGemini
	case strings.HasPrefix(mediaType, "audio/") || strings.HasPrefix(mediaType, "video/"):
		return d == DialectGemini && geminiMediaTypes[mediaType]
	}
	return false
}

var geminiMediaTypes = map[string]bool{
	"audio/aac": true, "audio/ogg": true, "audio/flac": true, "audio/aiff": true,
	"video/mp4": true, "video/mpeg": true, "video/quicktime": true, "video/webm": true,
	"video/x-flv": true, "video/x-ms-wmv": true, "video/3gpp": true,
}

func unwritten(d Dialect, img Image) error {
	return fmt.Errorf("the %s dialect writes no %s into a request, so the request that carried %q was not sent", d, img.MediaType, img.Name)
}
