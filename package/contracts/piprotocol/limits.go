// Composed prompt/transport bounds for LIMIT-01/X07-X08.
//
// Effective units and bounds at this level (all byte counts are serialized
// UTF-8 bytes unless noted as base64 characters):
//   - Frame/record: FrameMaxBytes / RecordMaxBytes (32 MiB serialized UTF-8
//     each). Not an allocation budget; decoded structures are bounded
//     separately and Unicode/escaping cannot bypass byte limits because len
//     counts UTF-8 bytes.
//   - Prompt text: PromptTextMaxBytes (4 MiB UTF-8) for the leading text
//     block. Matches the contracts text-input target.
//   - Images: at most PromptImageMaxCount (8); PromptImageMaxBase64Bytes
//     (4.5 MiB base64 characters) per image and
//     PromptImagesAggregateBase64Bytes (24 MiB base64 characters) aggregate.
//     Base64 length is not decoded-image memory; decoded pixels/allocation
//     are tracked separately by the caller.
//   - Text resources: at most PromptTextResourceMaxCount (4) files,
//     PromptTextResourceMaxBytes (1 MiB UTF-8) each and
//     PromptTextResourcesAggregateBytes (2 MiB UTF-8) aggregate, preserving
//     the current per-attachment compatibility baseline.
//   - Decoded structures: at most PromptMaxBlocks content blocks per prompt;
//     mime/name lengths bounded separately.
package piprotocol

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	FrameMaxBytes  = 32 * 1024 * 1024
	RecordMaxBytes = 32 * 1024 * 1024

	PromptTextMaxBytes = 4 * 1024 * 1024

	PromptImageMaxCount              = 8
	PromptImageMaxBase64Bytes        = 4718592 // 4.5 MiB in bytes-as-chars.
	PromptImagesAggregateBase64Bytes = 24 * 1024 * 1024

	PromptMaxBlocks = 16

	PromptTextResourceMaxCount        = 4
	PromptTextResourceMaxBytes        = 1024 * 1024
	PromptTextResourcesAggregateBytes = 2 * 1024 * 1024

	PromptMimeMaxBytes      = 128
	PromptSessionIDMaxBytes = 256
)

// ValidateSerializedFrame bounds one serialized frame/record in serialized
// UTF-8 bytes.
func ValidateSerializedFrame(payload []byte) error {
	if len(payload) > FrameMaxBytes {
		return fmt.Errorf("frame exceeds the %d-byte limit", FrameMaxBytes)
	}
	if !utf8.Valid(payload) {
		return fmt.Errorf("frame is not valid UTF-8")
	}
	return nil
}

// Validate checks composed prompt bounds without dispatching.
func (r PromptRequest) Validate() error {
	return ValidatePromptRequest(r)
}

// ValidatePromptRequest checks composed prompt bounds without dispatching.
// All string lengths are UTF-8 bytes (base64 characters for image data).
func ValidatePromptRequest(req PromptRequest) error {
	if len(req.SessionId) == 0 || len(req.SessionId) > PromptSessionIDMaxBytes {
		return fmt.Errorf("prompt session id out of bounds")
	}
	if len(req.Prompt) == 0 || len(req.Prompt) > PromptMaxBlocks {
		return fmt.Errorf("prompt exceeds %d blocks", PromptMaxBlocks)
	}
	images := 0
	imageBytes := 0
	resources := 0
	resourceBytes := 0
	sawText := false
	for _, block := range req.Prompt {
		switch {
		case block.Text != nil:
			text := block.Text.Text
			if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
				return fmt.Errorf("prompt text is invalid")
			}
			if len(text) > PromptTextMaxBytes {
				return fmt.Errorf("prompt text exceeds the %d-byte limit", PromptTextMaxBytes)
			}
			sawText = true
		case block.Image != nil:
			images++
			if images > PromptImageMaxCount {
				return fmt.Errorf("prompt images are limited to %d files", PromptImageMaxCount)
			}
			data := block.Image.Data
			if len(data) > PromptImageMaxBase64Bytes {
				return fmt.Errorf("prompt image exceeds the 4.5 MiB encoded size limit")
			}
			if !isPromptBase64(data) {
				return fmt.Errorf("malformed prompt image")
			}
			if len(block.Image.MimeType) == 0 || len(block.Image.MimeType) > PromptMimeMaxBytes {
				return fmt.Errorf("malformed prompt image")
			}
			switch block.Image.MimeType {
			case "image/png", "image/jpeg", "image/gif", "image/webp":
			default:
				return fmt.Errorf("malformed prompt image")
			}
			imageBytes += len(data)
			if imageBytes > PromptImagesAggregateBase64Bytes {
				return fmt.Errorf("prompt images exceed the 24 MiB aggregate encoded size limit")
			}
		case block.Resource != nil && block.Resource.Resource.TextResourceContents != nil:
			resources++
			if resources > PromptTextResourceMaxCount {
				return fmt.Errorf("prompt text attachments are limited to %d files", PromptTextResourceMaxCount)
			}
			contents := block.Resource.Resource.TextResourceContents
			if !utf8.ValidString(contents.Text) || strings.ContainsRune(contents.Text, 0) {
				return fmt.Errorf("malformed prompt text attachment")
			}
			if len(contents.Text) > PromptTextResourceMaxBytes {
				return fmt.Errorf("prompt text attachment exceeds the 1 MiB size limit")
			}
			resourceBytes += len(contents.Text)
			if resourceBytes > PromptTextResourcesAggregateBytes {
				return fmt.Errorf("prompt text attachments exceed the 2 MiB aggregate size limit")
			}
		default:
			return fmt.Errorf("malformed prompt block")
		}
	}
	if !sawText {
		return fmt.Errorf("prompt text is missing")
	}
	return nil
}

func isPromptBase64(value string) bool {
	if value == "" || len(value)%4 != 0 {
		return false
	}
	padding := 0
	if value[len(value)-1] == '=' {
		padding++
		if len(value) >= 2 && value[len(value)-2] == '=' {
			padding++
		}
	}
	for i := 0; i < len(value)-padding; i++ {
		if promptBase64Index(value[i]) < 0 {
			return false
		}
	}
	for i := len(value) - padding; i < len(value); i++ {
		if value[i] != '=' {
			return false
		}
	}
	if padding == 2 {
		return promptBase64Index(value[len(value)-3])&15 == 0
	}
	if padding == 1 {
		return promptBase64Index(value[len(value)-2])&3 == 0
	}
	return true
}

func promptBase64Index(value byte) int {
	switch {
	case value >= 'A' && value <= 'Z':
		return int(value - 'A')
	case value >= 'a' && value <= 'z':
		return int(value-'a') + 26
	case value >= '0' && value <= '9':
		return int(value-'0') + 52
	case value == '+':
		return 62
	case value == '/':
		return 63
	default:
		return -1
	}
}
