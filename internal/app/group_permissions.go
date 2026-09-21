package app

import (
	"encoding/json"
	"fmt"
	"riftledger/internal/sqlite"
)

func (s *Service) queueGroupMute(tx *sqlite.Tx, r Round) error {
	if !s.Config.MuteOnClose || s.Config.GroupID == 0 {
		return nil
	}
	if _, err := tx.Exec("INSERT INTO group_permissions(round_id,chat_id) VALUES(?,?)", r.ID, s.Config.GroupID); err != nil {
		return err
	}
	return queueGroupPermission(tx, r.ID, r.Number, s.Config.GroupID, "mute")
}
func (s *Service) queueGroupRestore(tx *sqlite.Tx, r Round) error {
	row, err := tx.One("SELECT chat_id FROM group_permissions WHERE round_id=?", r.ID)
	if err != nil || row == nil {
		return err
	}
	// Use the group captured at closing even if settings changed during the round.
	return queueGroupPermission(tx, r.ID, r.Number, row.Int("chat_id"), "restore")
}
func queueGroupPermission(tx *sqlite.Tx, round, number string, chat int64, action string) error {
	label := "封盘自动禁言群"
	if action == "restore" {
		label = "开奖后恢复群权限"
	}
	p := MessagePayload{GroupAction: action, RoundID: round, ChatID: chat, Text: fmt.Sprintf("%s期｜%s", number, label)}
	_, err := tx.Exec("INSERT OR IGNORE INTO outbox(key,chat_id,payload,created_at) VALUES(?,?,?,?)", "group-"+action+":"+round, chat, asJSON(p), now())
	return err
}

func (s *Service) GroupPermissionState(item OutboxItem) (original, roundState string, err error) {
	err = s.DB.Read(func(tx *sqlite.Tx) error {
		row, e := tx.One("SELECT g.original,r.state FROM group_permissions g JOIN rounds r ON r.id=g.round_id WHERE g.round_id=? AND g.chat_id=?", item.Payload.RoundID, item.Payload.ChatID)
		if e != nil {
			return e
		}
		if row == nil {
			return fmt.Errorf("群权限任务缺少期次记录，未修改群权限")
		}
		original, roundState = row["original"], row["state"]
		return nil
	})
	return
}

func (s *Service) SaveGroupPermissions(item OutboxItem, permissions map[string]bool) error {
	raw, err := json.Marshal(permissions)
	if err != nil {
		return err
	}
	return s.DB.Transaction(func(tx *sqlite.Tx) error {
		// Persist BEFORE the network mutation. Retries must never snapshot a muted group.
		_, e := tx.Exec("UPDATE group_permissions SET original=? WHERE round_id=? AND chat_id=? AND original=''", string(raw), item.Payload.RoundID, item.Payload.ChatID)
		return e
	})
}
