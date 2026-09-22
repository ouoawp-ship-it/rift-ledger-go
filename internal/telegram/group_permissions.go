package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"riftledger/internal/app"
	"strings"
)

func (c *Client) sendGroupPermission(ctx context.Context, s *app.Service, item app.OutboxItem) (bool, error) {
	detail, err := c.applyGroupPermission(ctx, s, item)
	state, retry := "SENT", int64(0)
	if err != nil {
		detail = "群管理失败：" + err.Error()
		state = "PENDING"
		retry = 5 * item.Attempts
		if item.Attempts >= 5 {
			state = "UNKNOWN"
		}
		var ae *APIError
		if errors.As(err, &ae) {
			if ae.Code == 429 {
				return true, c.rateLimited(s, item, ae.RetryAfter)
			} else if ae.Code >= 400 && ae.Code < 500 {
				state = "FAILED"
			}
		}
	}
	// Group permissions form a separate lane: failed notifications must not
	// prevent recovery, and missing admin rights must not block announcements.
	return true, persistDelivery(func() error { return s.CompleteOutbox(item, state, 0, detail, retry) })
}

func (c *Client) applyGroupPermission(ctx context.Context, s *app.Service, item app.OutboxItem) (string, error) {
	original, roundState, err := s.GroupPermissionState(item)
	if err != nil {
		return "", err
	}
	action := item.Payload.GroupAction
	if action != "mute" && action != "restore" {
		return "", &APIError{Code: 400, Description: "未知群管理操作"}
	}
	if action == "mute" && roundState != "CLOSED" {
		return "期次已结束，跳过迟到的封盘禁言", nil
	}
	if action == "restore" && original == "" {
		return "本期未执行禁言，无需恢复群权限", nil
	}
	if original == "" {
		var chat struct {
			Type        string          `json:"type"`
			Permissions map[string]bool `json:"permissions"`
		}
		if err = c.Call(ctx, "getChat", map[string]any{"chat_id": item.Payload.ChatID}, &chat); err != nil {
			return "", err
		}
		if (chat.Type != "group" && chat.Type != "supergroup") || len(chat.Permissions) == 0 {
			return "", &APIError{Code: 400, Description: "无法读取群原有权限，已取消自动禁言"}
		}
		if err = s.SaveGroupPermissions(item, chat.Permissions); err != nil {
			return "", err
		}
		original, roundState, err = s.GroupPermissionState(item)
		if err != nil {
			return "", err
		}
		if roundState != "CLOSED" {
			return "期次已结束，跳过迟到的封盘禁言", nil
		}
	}
	permissions := map[string]bool{}
	if err = json.Unmarshal([]byte(original), &permissions); err != nil || len(permissions) == 0 {
		return "", fmt.Errorf("原群权限记录损坏，未修改权限")
	}
	if action == "mute" {
		for key := range permissions {
			if strings.HasPrefix(key, "can_send_") || key == "can_add_web_page_previews" {
				permissions[key] = false
			}
		}
		for _, key := range []string{"can_send_messages", "can_send_audios", "can_send_documents", "can_send_photos", "can_send_videos", "can_send_video_notes", "can_send_voice_notes", "can_send_polls", "can_send_other_messages", "can_add_web_page_previews"} {
			permissions[key] = false
		}
	}
	var ok bool
	if err = c.Call(ctx, "setChatPermissions", map[string]any{"chat_id": item.Payload.ChatID, "permissions": permissions, "use_independent_chat_permissions": true}, &ok); err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("Telegram没有确认权限变更，将重试")
	}
	if action == "restore" {
		return "已恢复封盘前的群权限", nil
	}
	return "已限制普通成员发言", nil
}
