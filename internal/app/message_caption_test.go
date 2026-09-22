package app

import (
	"encoding/json"
	"riftledger/internal/sqlite"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestPhotoCaptionLayout(t *testing.T) {
	text := strings.Repeat("中", 1023) + "🎉" + strings.Repeat("后", 4100)
	parts := layoutMessageBlocks([]MessageBlock{{Type: "text", Text: "前言"}, {Type: "image", ImageID: "one"}, {Type: "text", Text: text}, {Type: "text", Text: "末段"}, {Type: "image", ImageID: "two"}, {Type: "text", Text: "第二张图"}})
	if len(parts) != 5 || parts[0].Text != "前言" || parts[1].ImageID != "one" || parts[4].ImageID != "two" || parts[4].Text != "第二张图" {
		t.Fatal(parts)
	}
	if parts[1].Text+parts[2].Text+parts[3].Text != text+"\n\n末段" {
		t.Fatal("lost text or emoji")
	}
	for _, p := range parts {
		limit := 4000
		if p.Type == "image" {
			limit = 1024
		}
		if len(utf16.Encode([]rune(p.Text))) > limit {
			t.Fatal("limit exceeded")
		}
	}
}

func TestCombinedTemplatePreviewQueueAndKeyboard(t *testing.T) {
	s, r := fixture(t, "SETTLE", "REFUND", "HOUSE")
	s.Config.GroupID = -100
	s.Config.BotUsername = "example_bot"
	imageID := templateAsset(t, s)
	patch := TemplatePatch{ID: "round_close", Blocks: []MessageBlock{{Type: "image", ImageID: imageID}, {Type: "text", Text: "{当前期数}\n已封盘 🔔"}}}
	saved := exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.SaveMessageTemplate(tx, patch) }).(MessageTemplate)
	if saved.Blocks[0].Text != "" {
		t.Fatal("image inherited default text", saved.Blocks)
	}
	patch.Blocks = saved.Blocks
	preview, err := s.PreviewMessageTemplate(patch)
	if err != nil || len(preview.([]MessageBlock)) != 1 || preview.([]MessageBlock)[0].Text != "2026-09-21-0007期\n已封盘 🔔" {
		t.Fatal(preview, err)
	}
	values := map[string]string{"当前期数": r.Number + "期"}
	exec(t, s, func(tx *sqlite.Tx) (any, error) {
		return nil, s.queueTemplate(tx, "caption-test", -100, "round_close", values, true)
	})
	rows, err := s.DB.Query("SELECT payload FROM outbox WHERE key LIKE 'caption-test%'")
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	var p MessagePayload
	if err = json.Unmarshal([]byte(rows[0]["payload"]), &p); err != nil {
		t.Fatal(err)
	}
	if p.Caption != r.Number+"期\n已封盘 🔔" || p.Text != p.Caption || p.ImageID != imageID || p.Markup == nil {
		t.Fatal(p)
	}
}
