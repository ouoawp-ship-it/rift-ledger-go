package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"riftledger/internal/app"
)

type Client struct {
	Token   string
	BaseURL string
	HTTP    *http.Client
	paced   bool
}
type APIError struct {
	Code        int
	Description string
	RetryAfter  int64
}

// Network and incomplete response failures can be retried during read-only startup.
type connectionError struct {
	message string
	unsent  bool
}

func requestNetworkError(method string, err error) error {
	var dns *net.DNSError
	var op *net.OpError
	unsent := errors.As(err, &dns) || (errors.As(err, &op) && op.Op == "dial")
	return &connectionError{message: fmt.Sprintf("Telegram %s 网络请求失败（%s）", method, networkReason(err)), unsent: unsent}
}
func retryUnsent(err error, attempt int64) (int64, bool) {
	var network *connectionError
	if !errors.As(err, &network) || !network.unsent {
		return 0, false
	}
	delay := int64(5)
	for i := int64(1); i < attempt && delay < 60; i++ {
		delay *= 2
	}
	if delay > 60 {
		delay = 60
	}
	return delay, true
}
func (e *connectionError) Error() string { return e.message }

func ConnectionRetryDelay(err error) (time.Duration, bool) {
	var network *connectionError
	if errors.As(err, &network) {
		return 5 * time.Second, true
	}
	var api *APIError
	if errors.As(err, &api) {
		if api.Code == 429 {
			delay := 5 * time.Second
			if api.RetryAfter > 5 {
				delay = time.Duration(api.RetryAfter) * time.Second
			}
			return delay, true
		}
		if api.Code >= 500 {
			return 5 * time.Second, true
		}
	}
	// Invalid credentials, conflicting consumers/webhooks, identity and DB errors need attention.
	return 0, false
}

func (e *APIError) Error() string { return fmt.Sprintf("Telegram错误%d: %s", e.Code, e.Description) }
func New(token string) *Client {
	return &Client{Token: token, BaseURL: "https://api.telegram.org", HTTP: &http.Client{Timeout: 40 * time.Second}}
}

func networkReason(err error) string {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "DNS解析失败"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "连接超时"
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if strings.Contains(strings.ToLower(urlErr.Err.Error()), "connection refused") {
			return "连接被拒绝"
		}
		if strings.Contains(strings.ToLower(urlErr.Err.Error()), "no route") {
			return "没有到目标的网络路由"
		}
	}
	return "网络不可达或TLS连接失败"
}

func (c *Client) Call(ctx context.Context, method string, payload any, result any) error {
	body, e := json.Marshal(payload)
	if e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/bot"+c.Token+"/"+method, bytes.NewReader(body))
	if e != nil {
		return errors.New("无法构造Telegram请求")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, e := c.HTTP.Do(req)
	if e != nil {
		return requestNetworkError(method, e)
	}
	return c.decodeResponse(resp, result)
}
func (c *Client) decodeResponse(resp *http.Response, result any) error {
	defer resp.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if e != nil {
		return &connectionError{message: "Telegram响应读取失败，结果未知"}
	}
	var envelope struct {
		OK          *bool           `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Code        int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  struct {
			RetryAfter int64 `json:"retry_after"`
		} `json:"parameters"`
	}
	if e = json.Unmarshal(raw, &envelope); e != nil {
		return &connectionError{message: "Telegram响应格式异常，结果未知"}
	}
	if envelope.OK == nil {
		return &connectionError{message: "Telegram响应缺少状态，结果未知"}
	}
	if !*envelope.OK {
		code := envelope.Code
		if code == 0 {
			code = resp.StatusCode
		}
		return &APIError{code, strings.ReplaceAll(envelope.Description, c.Token, "[已隐藏]"), envelope.Parameters.RetryAfter}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &connectionError{message: "Telegram响应状态异常，结果未知"}
	}
	if result != nil {
		if len(envelope.Result) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Result), []byte("null")) {
			return &connectionError{message: "Telegram响应缺少结果，结果未知"}
		}
		if err := json.Unmarshal(envelope.Result, result); err != nil {
			return &connectionError{message: "Telegram结果格式异常，结果未知"}
		}
		return nil
	}
	return nil
}
func wait(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Run never deletes webhooks and never clears the pending update queue.
// Keep one bot process and one consumer per token. A 409 stops reception.
func (c *Client) Run(ctx context.Context, s *app.Service) error {
	var me struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	}
	if e := c.Call(ctx, "getMe", map[string]any{}, &me); e != nil {
		return e
	}
	if s.Config.BotUsername != "" && !strings.EqualFold(me.Username, s.Config.BotUsername) {
		return errors.New("TG_BOT_USERNAME与Token身份不匹配，拒绝启动")
	}
	if e := s.BindBot(me.ID); e != nil {
		return e
	}
	var hook struct {
		URL string `json:"url"`
	}
	if e := c.Call(ctx, "getWebhookInfo", map[string]any{}, &hook); e != nil {
		return e
	}
	if hook.URL != "" {
		return errors.New("当前Bot已有Webhook，未擅自删除；请停用旧接收器并人工处理后再运行")
	}
	senderCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		sender := *c
		sender.paced = true
		var workers sync.WaitGroup
		for i := 0; i < 4; i++ {
			workers.Add(1)
			go func() { defer workers.Done(); sender.sendLoop(senderCtx, s) }()
		}
		workers.Wait()
	}()
	defer func() { cancel(); <-done }()
	_ = s.BotStatus("已连接 @" + me.Username)
	for ctx.Err() == nil {
		offset, e := s.Offset()
		if e != nil {
			return e
		}
		var updates []app.TGUpdate
		e = c.Call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": 25, "limit": 50, "allowed_updates": []string{"message", "callback_query"}}, &updates)
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			var ae *APIError
			if errors.As(e, &ae) && (ae.Code == 409 || ae.Code == 401) {
				return e
			}
			_ = s.BotStatus("接收异常，将重试：" + e.Error())
			delay, retry := ConnectionRetryDelay(e)
			if !retry {
				return e
			}
			slog.Warn("Telegram轮询失败，自动重试", "error", e, "retry_seconds", delay.Seconds())
			if !wait(ctx, delay) {
				return nil
			}
			continue
		}
		_ = s.BotStatus("接收正常｜最近轮询 " + time.Now().UTC().Format(time.RFC3339))
		for _, u := range updates {
			if e = s.HandleUpdate(u); e != nil {
				slog.Error("处理Telegram更新失败；未推进偏移", "update_id", u.ID, "error", e)
				if !wait(ctx, 2*time.Second) {
					return nil
				}
				break
			}
			if u.Callback != nil {
				_ = c.Call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": u.Callback.ID}, nil)
			}
		}
	}
	return nil
}
func (c *Client) SendOne(ctx context.Context, s *app.Service) (bool, error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	item, e := s.ClaimOutbox()
	if e != nil || item == nil {
		return false, e
	}
	// Let an already claimed request finish during a graceful process restart.
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	ctx = sendCtx
	if len(item.Payload.Media) > 0 {
		return c.sendMedia(ctx, s, *item)
	}
	payload := map[string]any{"chat_id": item.Payload.ChatID, "text": item.Payload.Text}
	if item.Payload.Markup != nil {
		payload["reply_markup"] = item.Payload.Markup
	}
	method := "sendMessage"
	if item.CardMessageID > 0 {
		method = "editMessageText"
		payload["message_id"] = item.CardMessageID
	}
	var result struct {
		MessageID int64 `json:"message_id"`
	}
	e = c.Call(ctx, method, payload, &result)
	state, detail, retry := "SENT", "", int64(0)
	id := result.MessageID
	if e != nil {
		state = "UNKNOWN"
		detail = e.Error()
		if delay, safe := retryUnsent(e, item.Attempts); safe {
			state, retry = "PENDING", delay
		}
		var ae *APIError
		if errors.As(e, &ae) {
			if ae.Code == 400 && strings.Contains(strings.ToLower(ae.Description), "message is not modified") && item.CardMessageID > 0 {
				state = "SENT"
				id = item.CardMessageID
				detail = "卡片内容已一致"
			} else if ae.Code == 429 {
				state = "PENDING"
				retry = ae.RetryAfter
				if retry < 1 {
					retry = 5
				}
			} else if ae.Code >= 400 && ae.Code < 500 {
				state = "FAILED"
			}
		}
	} else if id <= 0 {
		state = "UNKNOWN"
		detail = "成功响应没有message_id，请核实送达"
	}
	if state == "SENT" && c.paced {
		retry = 1
		if item.Payload.ChatID < 0 {
			retry = 3
		}
	}
	if e != nil {
		slog.Warn("Telegram发送状态", "outbox_id", item.ID, "state", state, "detail", detail)
	}
	// Use a fresh local transaction even if the network request's context was canceled.
	if err := s.CompleteOutbox(*item, state, id, detail, retry); err != nil {
		return true, err
	}
	return true, nil
}
func (c *Client) sendLoop(ctx context.Context, s *app.Service) {
	for ctx.Err() == nil {
		sent, e := c.SendOne(ctx, s)
		if e != nil {
			slog.Error("消息队列处理失败", "error", e)
		}
		d := 500 * time.Millisecond
		if sent {
			d = time.Second
		}
		if !wait(ctx, d) {
			return
		}
	}
}

// The five heroes are composed into ONE image: Telegram sendPhoto accepts it;
// sendMediaGroup requires 2–10 media items and rejects this payload.
func (c *Client) sendMedia(ctx context.Context, s *app.Service, item app.OutboxItem) (bool, error) {
	var result struct {
		MessageID int64 `json:"message_id"`
	}
	var e error
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if len(item.Payload.Media) != 5 || s.Champions == nil {
		e = errors.New("英雄缓存不可用")
	} else {
		photos := make([][]byte, 0, 5)
		for _, p := range item.Payload.Media {
			var data []byte
			data, e = s.Champions.Image(p.Version, p.ID)
			if e != nil {
				break
			}
			photos = append(photos, data)
		}
		if e == nil {
			var composite []byte
			composite, e = composeHeroImage(photos, item.Payload.Media)
			if e == nil {
				var part io.Writer
				part, e = writer.CreateFormFile("photo", "heroes.png")
				if e == nil {
					_, e = part.Write(composite)
				}
			}
		}
	}
	localFailure := e != nil
	if e == nil {
		_ = writer.WriteField("caption", item.Payload.Text)
		_ = writer.WriteField("chat_id", fmt.Sprint(item.Payload.ChatID))
		_ = writer.Close()
		var req *http.Request
		req, e = http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/bot"+c.Token+"/sendPhoto", &body)
		if e == nil {
			req.Header.Set("Content-Type", writer.FormDataContentType())
			var resp *http.Response
			resp, e = c.HTTP.Do(req)
			if e != nil {
				e = requestNetworkError("sendPhoto", e)
			} else {
				e = c.decodeResponse(resp, &result)
			}
		} else {
			e = errors.New("无法构造图片请求")
		}
	}
	if e == nil && result.MessageID > 0 {
		raw, _ := json.Marshal([]any{result})
		cooldown := int64(0)
		if c.paced {
			cooldown = 1
			if item.Payload.ChatID < 0 {
				cooldown = 3
			}
		}
		return true, s.CompleteMedia(item, "SENT", result.MessageID, "", string(raw), cooldown)
	}
	if delay, safe := retryUnsent(e, item.Attempts); safe {
		return true, s.CompleteMedia(item, "PENDING", 0, e.Error(), "", delay)
	}
	var ae *APIError
	if errors.As(e, &ae) && ae.Code == 429 {
		retry := ae.RetryAfter
		if retry < 1 {
			retry = 5
		}
		return true, s.CompleteMedia(item, "PENDING", 0, "图片发送限流", "", retry)
	}
	detail := "图片发送结果未知；已排队纯文字公告，请核实图片是否送达"
	state := "UNKNOWN"
	if localFailure || (errors.As(e, &ae) && ae.Code >= 400 && ae.Code < 500) {
		state = "FAILED"
		detail = "图片发送失败，已降级纯文字公告"
	}
	if ae != nil {
		detail += "：" + ae.Error()
	}
	slog.Warn("Telegram图片降级", "outbox_id", item.ID, "state", state, "detail", detail)
	return true, s.FallbackMedia(item, state, detail)
}
