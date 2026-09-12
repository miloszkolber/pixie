package piprotocol_test

import (
	"strings"
	"testing"

	piwire "github.com/miloszkolber/pixie/contracts/piprotocol"
)

func validBase64Chars(n int) string {
	return strings.Repeat("A", n)
}

func TestPromptImageCapRejectsOversizeFrame(t *testing.T) {
	oversize := validBase64Chars(piwire.PromptImageMaxBase64Bytes + 4)
	req := piwire.PromptRequest{
		SessionId: "session",
		Prompt:    []piwire.ContentBlock{piwire.TextBlock("hello"), piwire.ImageBlock(oversize, "image/png")},
	}
	if err := req.Validate(); err == nil {
		t.Fatal("oversize image frame was admitted")
	}
	boundary := validBase64Chars(piwire.PromptImageMaxBase64Bytes)
	ok := piwire.PromptRequest{
		SessionId: "session",
		Prompt:    []piwire.ContentBlock{piwire.TextBlock("hello"), piwire.ImageBlock(boundary, "image/png")},
	}
	if err := ok.Validate(); err != nil {
		t.Fatalf("boundary image rejected: %v", err)
	}
}

func TestPromptAggregateCapRejectsOverflow(t *testing.T) {
	each := (piwire.PromptImagesAggregateBase64Bytes / 6) + 4
	blocks := []piwire.ContentBlock{piwire.TextBlock("hello")}
	for i := 0; i < 6; i++ {
		blocks = append(blocks, piwire.ImageBlock(validBase64Chars(each), "image/png"))
	}
	req := piwire.PromptRequest{SessionId: "session", Prompt: blocks}
	if err := req.Validate(); err == nil {
		t.Fatal("aggregate image cap overflow was admitted")
	}
	if got := piwire.PromptImageMaxCount; got != 8 {
		t.Fatalf("image count bound moved: %d", got)
	}
	many := []piwire.ContentBlock{piwire.TextBlock("hello")}
	for i := 0; i < piwire.PromptImageMaxCount+1; i++ {
		many = append(many, piwire.ImageBlock(validBase64Chars(4), "image/png"))
	}
	if err := (piwire.PromptRequest{SessionId: "session", Prompt: many}).Validate(); err == nil {
		t.Fatal("image count cap overflow was admitted")
	}
}

func TestPromptTextBoundCountsUTF8Bytes(t *testing.T) {
	over := strings.Repeat("é", piwire.PromptTextMaxBytes/2+1)
	req := piwire.PromptRequest{
		SessionId: "session",
		Prompt:    []piwire.ContentBlock{piwire.TextBlock(over)},
	}
	if err := req.Validate(); err == nil {
		t.Fatal("oversize Unicode text was admitted by rune count")
	}
	if err := piwire.ValidateSerializedFrame(make([]byte, piwire.FrameMaxBytes+1)); err == nil {
		t.Fatal("oversize serialized frame was admitted")
	}
	boundary := strings.Repeat("a", piwire.PromptTextMaxBytes)
	ok := piwire.PromptRequest{
		SessionId: "session",
		Prompt:    []piwire.ContentBlock{piwire.TextBlock(boundary)},
	}
	if err := ok.Validate(); err != nil {
		t.Fatalf("boundary text rejected: %v", err)
	}
}

func TestPromptResourceAndBlockBoundsRejectBeforeDispatch(t *testing.T) {
	resourceText := strings.Repeat("x", piwire.PromptTextResourceMaxBytes+1)
	resource := piwire.ResourceBlock(piwire.EmbeddedResourceResource{TextResourceContents: &piwire.TextResourceContents{
		Uri: "pixie://attachment/file.txt", Text: resourceText,
	}})
	request := piwire.PromptRequest{SessionId: "session", Prompt: []piwire.ContentBlock{piwire.TextBlock("hello"), resource}}
	if err := request.Validate(); err == nil {
		t.Fatal("oversize text resource was admitted")
	}
	blocks := make([]piwire.ContentBlock, 0, piwire.PromptMaxBlocks+1)
	for i := 0; i < piwire.PromptMaxBlocks+1; i++ {
		blocks = append(blocks, piwire.TextBlock("x"))
	}
	if err := (piwire.PromptRequest{SessionId: "session", Prompt: blocks}).Validate(); err == nil {
		t.Fatal("oversize decoded block structure was admitted")
	}
}
