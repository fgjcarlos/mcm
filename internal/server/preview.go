package server

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

func formatPayloadPreview(payload []byte, limit int) (string, bool, bool) {
	if limit <= 0 {
		limit = MaxPayloadPreviewBytes
	}

	text := strings.ToValidUTF8(string(payload), "\uFFFD")
	isJSON := false

	if json.Valid(payload) {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, payload, "", "  "); err == nil {
			text = pretty.String()
			isJSON = true
		}
	}

	preview, truncated := truncateUTF8(text, limit)
	return preview, truncated, isJSON
}

func truncateUTF8(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}

	if limit <= 3 {
		return value[:limit], true
	}

	cut := limit - 3
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}

	return value[:cut] + "...", true
}
