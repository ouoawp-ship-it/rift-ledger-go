package app

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"riftledger/internal/sqlite"
	"strings"
	"testing"
	"unicode/utf16"
)

func templateAsset(t *testing.T, s *Service) string {
	t.Helper()
	var b bytes.Buffer
	if e := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 20, 20))); e != nil {
		t.Fatal(e)
	}
	asset, e := s.SaveMessageImage(b.Bytes())
	if e != nil {
		t.Fatal(e)
	}
	id := asset.(map[string]any)["id"].(string)
	raw, mime, e := s.MessageImage(id)
	if e != nil || mime != "image/png" || !bytes.Equal(raw, b.Bytes()) {
		t.Fatal("asset mismatch", e)
	}
	again, e := s.SaveMessageImage(b.Bytes())
	if e != nil || again.(map[string]any)["id"] != id {
		t.Fatal("upload must deduplicate", e)
	}
	return id
}
func TestTemplateValidationAndSnapshots(t *testing.T) {
	s, r := fixture(t, "SETTLE", "REFUND", "HOUSE")
	s.Config.GroupID = -100
	imageID := templateAsset(t, s)
	for _, text := range []string{"遗漏期数", "{当前期数} {余额}", "{当前期数} {", "{当前期数} " + strings.Repeat("长", 2000)} {
		p := TemplatePatch{ID: "round_close", Blocks: []MessageBlock{{Type: "text", Text: text}}}
		expectFailure(t, s, func(tx *sqlite.Tx) (any, error) { return s.SaveMessageTemplate(tx, p) })
	}
	p := TemplatePatch{ID: "round_close", Blocks: []MessageBlock{{Type: "text", Text: "🔔 {当前期数}"}, {Type: "image", ImageID: imageID}, {Type: "text", Text: "请等待开奖 🎉"}}}
	d := exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.SaveMessageTemplate(tx, p) }).(MessageTemplate)
	if d.Revision != 1 {
		t.Fatal(d)
	}
	expectFailure(t, s, func(tx *sqlite.Tx) (any, error) { return s.SaveMessageTemplate(tx, p) })
	exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.CloseRound(tx, r.ID) })
	rows, e := s.DB.Query("SELECT key,payload FROM outbox WHERE key LIKE 'round-close:%' ORDER BY id")
	if e != nil || len(rows) != 3 {
		t.Fatal(rows, e)
	}
	if !strings.Contains(rows[0]["payload"], r.Number+"期") || strings.Contains(rows[0]["payload"], "{当前期数}") {
		t.Fatal(rows)
	}
	var photo MessagePayload
	_ = json.Unmarshal([]byte(rows[1]["payload"]), &photo)
	if photo.ImageID != imageID {
		t.Fatal(photo)
	}
	p.Revision = 1
	p.Blocks = []MessageBlock{{Type: "text", Text: "新版 {当前期数}"}}
	exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.SaveMessageTemplate(tx, p) })
	exec(t, s, func(tx *sqlite.Tx) (any, error) {
		return nil, s.queueTemplate(tx, "round-close:"+r.ID, -100, "round_close", map[string]string{"当前期数": "新值"}, false)
	})
	after, _ := s.DB.Query("SELECT key,payload FROM outbox WHERE key LIKE 'round-close:%' ORDER BY id")
	if asJSON(rows) != asJSON(after) {
		t.Fatal("editing/replaying changed captured messages")
	}
	item, e := s.ClaimOutbox()
	if e != nil || item == nil || !strings.Contains(item.Payload.Text, r.Number) {
		t.Fatal(item, e)
	}
	blocked, e := s.ClaimOutbox()
	if e != nil || blocked != nil {
		t.Fatal("photo must wait for earlier text", e)
	}
	if e = s.CompleteOutbox(*item, "SENT", 1, "", 0); e != nil {
		t.Fatal(e)
	}
	item, e = s.ClaimOutbox()
	if e != nil || item == nil || item.Payload.ImageID != imageID {
		t.Fatal(item, e)
	}
	// Restart after an ambiguous photo upload never resends it or blocks the text.
	if e = initialize(s.DB); e != nil {
		t.Fatal(e)
	}
	item, e = s.ClaimOutbox()
	if e != nil || item == nil || item.Payload.Text != "请等待开奖 🎉" {
		t.Fatal("restart blocked subsequent text", item, e)
	}
}
func TestTemplateCatalogAndLiteralValues(t *testing.T) {
	s, _ := fixture(t, "SETTLE", "REFUND", "HOUSE")
	for _, d := range templateDefinitions() {
		p := TemplatePatch{ID: d.ID, Blocks: d.Defaults}
		if _, e := s.PreviewMessageTemplate(p); e != nil {
			t.Fatalf("%s: %v", d.ID, e)
		}
	}
	blocks := renderBlocks([]MessageBlock{{Type: "text", Text: "{玩家昵称} {当前期数}"}}, map[string]string{"玩家昵称": "{当前期数}<script>", "当前期数": "0001期"})
	if blocks[0].Text != "{当前期数}<script> 0001期" {
		t.Fatal("variable values reinterpreted", blocks)
	}
	input := strings.Repeat("🎉中文", 4000)
	parts := splitMessage(input)
	if strings.Join(parts, "") != input {
		t.Fatal("split lost content")
	}
	for _, s := range parts {
		if len(utf16.Encode([]rune(s))) > 4000 {
			t.Fatal("exceeds Telegram text limit")
		}
	}
	for _, raw := range [][]byte{[]byte("<svg></svg>"), make([]byte, MaxMessageImage+1)} {
		if _, e := s.SaveMessageImage(raw); e == nil {
			t.Fatal("accepted invalid asset")
		}
	}
	// Schema 4 already stores thousandths; upgrading and reopening must not rescale.
	before := acc(t, s, "tg:111").Balance
	if _, e := s.DB.Exec("UPDATE meta SET value='4' WHERE key='schema_version'"); e != nil {
		t.Fatal(e)
	}
	if e := initialize(s.DB); e != nil {
		t.Fatal(e)
	}
	if e := initialize(s.DB); e != nil {
		t.Fatal(e)
	}
	if after := acc(t, s, "tg:111").Balance; after != before {
		t.Fatal("migration rescaled existing balances", before, after)
	}
}
