package telegram

import (
	"context"
	"log/slog"
	"riftledger/internal/app"
	"time"
)

// Keep the known response in this worker and retry only the local commit.
// Reissuing sendMessage after a successful remote response would duplicate it.
func persistDelivery(save func() error) error {
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		if err = save(); err == nil {
			return nil
		}
		if attempt == 0 {
			slog.Warn("发送结果暂未保存，重试本地提交；不重发网络请求", "error", err)
		}
		if attempt < 3 {
			time.Sleep(time.Duration(100*(1<<attempt)) * time.Millisecond)
		}
	}
	slog.Error("发送结果落库失败，保留发送中状态；请检查数据库后重启并核实送达", "error", err)
	return err
}

func (c *Client) rateLimited(s *app.Service, item app.OutboxItem, seconds int64) error {
	if seconds < 1 {
		seconds = 5
	}
	slog.Warn("Telegram发送限流，统一等待", "outbox_id", item.ID, "retry_seconds", seconds)
	// Gate already claimed workers immediately, before the durable update.
	if c.scheduler != nil {
		c.scheduler.pause(time.Now().Add(time.Duration(seconds) * time.Second))
	}
	return persistDelivery(func() error {
		_, err := s.RateLimitOutbox(item, seconds)
		return err
	})
}

// readRetryDelay increases repeated read failures without ever replaying writes.
func readRetryDelay(err error, failures int) (time.Duration, bool) {
	delay, retry := ConnectionRetryDelay(err)
	if !retry {
		return 0, false
	}
	backoff := 5 * time.Second
	for i := 1; i < failures && backoff < 30*time.Second; i++ {
		backoff *= 2
	}
	if backoff > 30*time.Second {
		backoff = 30 * time.Second
	}
	if backoff > delay {
		delay = backoff
	}
	return delay, true
}

// Kept separate from send deadlines: cancellation never starts another network send.
func (c *Client) waitToSend(ctx context.Context, s *app.Service, item app.OutboxItem) (bool, error) {
	if c.paced && c.scheduler != nil && !c.scheduler.wait(ctx, item.Payload.ChatID) {
		return false, persistDelivery(func() error {
			return s.CompleteOutbox(item, "PENDING", 0, "尚未发出请求，等待下次发送", 1)
		})
	}
	deferred, err := s.DeferLimitedOutbox(item)
	return !deferred && err == nil, err
}
