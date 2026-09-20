package app

import (
	"strconv"
	"strings"

	"riftledger/internal/sqlite"
)

// Allow a full Telegram long poll (25s), HTTP timeout (40s), and retry delay.
const botStatusMaxAge int64 = 75

type BotConnection struct {
	State     string `json:"state"`
	Message   string `json:"message"`
	UpdatedAt int64  `json:"updated_at"`
	CheckedAt int64  `json:"checked_at"`
}

// Read the running receiver's status, not saved settings or a one-off getMe test.
func (s *Service) BotConnection() (BotConnection, error) {
	status := BotConnection{State: "disabled", Message: "未启用Telegram", CheckedAt: now()}
	err := s.DB.Read(func(tx *sqlite.Tx) error {
		rows, e := tx.Query("SELECT key,value FROM meta WHERE key IN ('bot_status','bot_status_at')")
		if e != nil {
			return e
		}
		for _, row := range rows {
			if row["key"] == "bot_status" {
				status.Message = row["value"]
			} else {
				status.UpdatedAt, _ = strconv.ParseInt(row["value"], 10, 64)
			}
		}
		return nil
	})
	if err != nil {
		return status, err
	}
	switch {
	case strings.HasPrefix(status.Message, "未启用Telegram"):
		status.State = "disabled"
	case strings.HasPrefix(status.Message, "接收正常"):
		status.State = "online"
	case strings.HasPrefix(status.Message, "正在连接"), strings.HasPrefix(status.Message, "已连接 @"):
		status.State = "connecting"
	default:
		status.State = "error"
	}
	if (status.State == "online" || status.State == "connecting") &&
		(status.UpdatedAt <= 0 || status.CheckedAt-status.UpdatedAt > botStatusMaxAge || status.UpdatedAt > status.CheckedAt) {
		status.State = "stale"
		status.Message = "机器人连接状态已超时，等待接收器恢复"
	}
	return status, nil
}
