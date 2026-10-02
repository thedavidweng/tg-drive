package telegramgotd

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	tgtelegram "github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// RateLimiter paces outbound Telegram RPCs to reduce FLOOD_WAIT triggers and
// respect FLOOD_WAIT_X when it does occur. It is implemented as a gotd
// telegram.Middleware, so it intercepts every API call (message sends, history
// reads, file up/downloads, etc.) with a single mechanism.
//
// Design notes:
//   - Conservative base intervals for known chat-scoped mutation methods
//     (send/edit/delete) avoid the 1 msg/sec per chat limit.
//   - All other methods start with a small (or zero) base interval and adapt
//     after the first FLOOD_WAIT, then decay back toward their base.
//   - FLOOD_WAIT is handled reactively: the middleware sleeps the requested
//     duration and retries the same request, unless the wait would exceed the
//     configured maximum.
//   - Transient server errors (5xx, timeouts) are retried with bounded
//     exponential backoff, but only for idempotent methods: a send or edit
//     that failed server-side may still have been applied, and replaying it
//     could duplicate a post.
type RateLimiter struct {
	waitFlood  bool
	maxWait    time.Duration
	maxRetries int
	clock      func() time.Time

	transientBase time.Duration
	logf          func(format string, args ...any)

	mu      sync.Mutex
	methods map[methodKey]*methodState
}

// methodKey identifies a rate-limited stream. Methods can be global or
// per-chat: for message sends we scope by channel so unrelated channels do not
// pace each other.
type methodKey struct {
	method    string
	channelID int64
}

type methodState struct {
	mu          sync.Mutex
	base        time.Duration
	nextAllowed time.Time
	interval    time.Duration
}

// NewRateLimiter creates a rate limiter. waitFlood and maxWait mirror the CLI
// --wait/--no-wait behavior: when waitFlood is true, FLOOD_WAIT_X is slept and
// retried up to maxWait; otherwise it is returned as a FloodWaitError.
func NewRateLimiter(waitFlood bool, maxWait time.Duration) *RateLimiter {
	if maxWait <= 0 {
		maxWait = 300 * time.Second
	}
	return &RateLimiter{
		waitFlood:  waitFlood,
		maxWait:    maxWait,
		maxRetries: 10,
		clock:      time.Now,
		methods:    make(map[methodKey]*methodState),

		transientBase: 500 * time.Millisecond,
	}
}

// maxTransientRetries bounds retries of one idempotent RPC after transient
// server errors (0.5s, 1s, 2s with the default base).
const maxTransientRetries = 3

// SetLogger routes per-RPC diagnostics (method, latency, retries) to logf.
func (rl *RateLimiter) SetLogger(logf func(format string, args ...any)) { rl.logf = logf }

func (rl *RateLimiter) debugf(format string, args ...any) {
	if rl.logf != nil {
		rl.logf(format, args...)
	}
}

// Middleware returns a gotd telegram.Middleware that applies this rate limiter.
func (rl *RateLimiter) Middleware() telegram.Middleware {
	return telegram.MiddlewareFunc(func(next tg.Invoker) telegram.InvokeFunc {
		return rl.invoke(next)
	})
}

func (rl *RateLimiter) invoke(next tg.Invoker) telegram.InvokeFunc {
	return func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		method := fmt.Sprintf("%T", input)
		key := methodKey{method: method, channelID: extractChatID(input)}
		if base, perChat := methodBaseInterval(method); perChat {
			if key.channelID == 0 {
				// Could not extract a chat scope; fall back to method-global.
				key.channelID = 0
				key.method = method
			} else {
				_ = base
			}
		} else {
			key.channelID = 0
		}

		state, base := rl.stateFor(key)
		if err := state.reserve(ctx, rl.clock(), base, rl.maxWait); err != nil {
			return err
		}

		totalWaited := time.Duration(0)
		transientRetries := 0
		for attempt := 0; attempt <= rl.maxRetries; attempt++ {
			started := time.Now()
			err := next.Invoke(ctx, input, output)
			elapsed := time.Since(started).Round(time.Millisecond)
			if err == nil {
				rl.debugf("rpc %s ok in %s", shortMethod(method), elapsed)
				state.recordSuccess(base)
				return nil
			}
			rl.debugf("rpc %s failed in %s: %v", shortMethod(method), elapsed, err)

			if transientRetries < maxTransientRetries && idempotentMethod(method) && isTransient(err) {
				delay := rl.transientBase << transientRetries
				transientRetries++
				rl.debugf("rpc %s: transient error, retry %d/%d in %s", shortMethod(method), transientRetries, maxTransientRetries, delay)
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return ctx.Err()
				}
				continue
			}

			wait, ok := tgerr.AsFloodWait(err)
			if !ok || wait <= 0 {
				return err
			}

			state.recordFlood(wait, rl.maxWait)
			if !rl.waitFlood || totalWaited+wait > rl.maxWait {
				return &tgtelegram.FloodWaitError{Seconds: int(wait.Seconds())}
			}
			rl.debugf("rpc %s: flood wait, sleeping %s", shortMethod(method), wait)

			select {
			case <-time.After(wait):
				totalWaited += wait
			case <-ctx.Done():
				return ctx.Err()
			}

			if err := state.reserve(ctx, rl.clock(), base, rl.maxWait); err != nil {
				return err
			}
		}
		return &tgtelegram.FloodWaitError{Seconds: int((rl.maxWait - totalWaited).Seconds())}
	}
}

func (rl *RateLimiter) stateFor(key methodKey) (*methodState, time.Duration) {
	base, _ := methodBaseInterval(key.method)

	rl.mu.Lock()
	defer rl.mu.Unlock()

	if s, ok := rl.methods[key]; ok {
		return s, base
	}
	s := &methodState{base: base}
	rl.methods[key] = s
	return s, base
}

func (s *methodState) reserve(ctx context.Context, now time.Time, base, maxInterval time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.interval == 0 {
		s.interval = base
	}
	if s.interval > maxInterval {
		s.interval = maxInterval
	}

	if !s.nextAllowed.IsZero() {
		for {
			d := s.nextAllowed.Sub(now)
			if d <= 0 {
				break
			}
			s.mu.Unlock()
			select {
			case <-time.After(d):
			case <-ctx.Done():
				s.mu.Lock()
				return ctx.Err()
			}
			s.mu.Lock()
			now = time.Now()
		}
	}

	s.nextAllowed = now.Add(s.interval)
	return nil
}

func (s *methodState) recordSuccess(base time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.interval <= base {
		s.interval = base
		return
	}
	// Exponential decay toward the base: halve the excess each success.
	s.interval = (s.interval + base) / 2
	if s.interval < base {
		s.interval = base
	}
}

func (s *methodState) recordFlood(wait, maxInterval time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if wait > s.interval {
		s.interval = wait
	}
	if s.interval > maxInterval {
		s.interval = maxInterval
	}
	s.nextAllowed = time.Now().Add(s.interval)
}

func shortMethod(method string) string {
	return strings.TrimSuffix(strings.TrimPrefix(method, "*tg."), "Request")
}

// isTransient reports server-side failures that are expected to clear on
// their own: 5xx RPC errors and MTProto timeouts (code -503).
func isTransient(err error) bool {
	rpcErr, ok := tgerr.As(err)
	if !ok {
		return false
	}
	return rpcErr.Code >= 500 || rpcErr.Code == -503 || rpcErr.Code == -500 || rpcErr.IsType("TIMEOUT")
}

// idempotentMethod lists RPCs that are safe to replay: reads, plus file part
// uploads, which are keyed by (file_id, part) and overwrite on replay.
func idempotentMethod(method string) bool {
	switch method {
	case "*tg.MessagesGetHistoryRequest",
		"*tg.MessagesGetRepliesRequest",
		"*tg.MessagesGetDiscussionMessageRequest",
		"*tg.MessagesGetDialogsRequest",
		"*tg.MessagesGetSavedHistoryRequest",
		"*tg.MessagesGetSavedDialogsRequest",
		"*tg.MessagesGetMessagesRequest",
		"*tg.ChannelsGetMessagesRequest",
		"*tg.ChannelsGetChannelsRequest",
		"*tg.ChannelsGetFullChannelRequest",
		"*tg.UsersGetUsersRequest",
		"*tg.UsersGetFullUserRequest",
		"*tg.HelpGetConfigRequest",
		"*tg.UploadGetFileRequest",
		"*tg.UploadGetFileHashesRequest",
		"*tg.UploadSaveFilePartRequest",
		"*tg.UploadSaveBigFilePartRequest":
		return true
	}
	return false
}

// methodBaseInterval returns the per-chat interval and whether this method
// should be scoped by chat. Base intervals are intentionally conservative for
// message mutations; the rest adapt on first flood.
func methodBaseInterval(method string) (time.Duration, bool) {
	switch method {
	case "*tg.MessagesSendMediaRequest",
		"*tg.MessagesSendMultiMediaRequest",
		"*tg.MessagesSendMessageRequest",
		"*tg.MessagesEditMessageRequest",
		"*tg.ChannelsDeleteMessagesRequest":
		return 1100 * time.Millisecond, true
	case "*tg.ChannelsGetMessagesRequest",
		"*tg.MessagesGetHistoryRequest":
		return 200 * time.Millisecond, true
	case "*tg.MessagesExportChatInviteRequest":
		return 5 * time.Second, true
	case "*tg.MessagesGetDialogsRequest":
		return 1 * time.Second, false
	case "*tg.AuthSendCodeRequest":
		return 60 * time.Second, false
	case "*tg.UploadGetFileRequest",
		"*tg.UploadGetCdnFileRequest",
		"*tg.UploadSaveFilePartRequest",
		"*tg.UploadSaveBigFilePartRequest",
		"*tg.UploadReuploadCdnFileRequest",
		"*tg.UploadGetCdnFileHashesRequest",
		"*tg.UploadGetFileHashesRequest":
		return 20 * time.Millisecond, false
	case "*tg.ChannelsGetChannelsRequest",
		"*tg.ChannelsGetFullChannelRequest":
		return 500 * time.Millisecond, true
	default:
		return 0, false
	}
}

// extractChatID returns the channel/chat ID for methods where rate limits are
// per conversation. Returns 0 for global methods or unsupported inputs.
func extractChatID(input bin.Encoder) int64 {
	switch req := input.(type) {
	case *tg.MessagesSendMediaRequest:
		return peerChannelID(req.Peer)
	case *tg.MessagesSendMultiMediaRequest:
		return peerChannelID(req.Peer)
	case *tg.MessagesSendMessageRequest:
		return peerChannelID(req.Peer)
	case *tg.MessagesEditMessageRequest:
		return peerChannelID(req.Peer)
	case *tg.MessagesGetHistoryRequest:
		return peerChannelID(req.Peer)
	case *tg.MessagesExportChatInviteRequest:
		return peerChannelID(req.Peer)
	case *tg.ChannelsDeleteMessagesRequest:
		return inputChannelID(req.Channel)
	case *tg.ChannelsGetMessagesRequest:
		return inputChannelID(req.Channel)
	case *tg.ChannelsGetChannelsRequest:
		if len(req.ID) == 1 {
			return inputChannelID(req.ID[0])
		}
	case *tg.ChannelsGetFullChannelRequest:
		return inputChannelID(req.Channel)
	case *tg.ChannelsExportMessageLinkRequest:
		return inputChannelID(req.Channel)
	}
	return 0
}

func peerChannelID(p tg.InputPeerClass) int64 {
	if p == nil {
		return 0
	}
	switch v := p.(type) {
	case *tg.InputPeerChannel:
		return v.ChannelID
	case *tg.InputPeerChannelFromMessage:
		return v.ChannelID
	}
	return 0
}

func inputChannelID(c tg.InputChannelClass) int64 {
	if c == nil {
		return 0
	}
	if v, ok := c.(*tg.InputChannel); ok {
		return v.ChannelID
	}
	return 0
}
