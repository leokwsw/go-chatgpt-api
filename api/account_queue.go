package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/linweiyuan/go-logger/logger"
)

type accountQueueClaims struct {
	Sub  string `json:"sub"`
	Auth struct {
		UserID string `json:"user_id"`
	} `json:"https://api.openai.com/auth"`
	Profile struct {
		Email string `json:"email"`
	} `json:"https://api.openai.com/profile"`
}

type accountRequestQueue struct {
	mu      sync.Mutex
	active  bool
	waiters []chan struct{}
	refs    int
}

type accountQueueRegistry struct {
	mu     sync.Mutex
	queues map[string]*accountRequestQueue
}

var sharedAccountQueues = accountQueueRegistry{
	queues: make(map[string]*accountRequestQueue),
}

// AcquireAccountRequest serializes ChatGPT-facing work for the same account key.
func AcquireAccountRequest(ctx context.Context, accessToken string) (func(), error) {
	queueKey := accountQueueKey(accessToken)
	if queueKey == "" {
		return func() {}, nil
	}

	queue := sharedAccountQueues.retain(queueKey)
	waitStartedAt := time.Now()
	waited, err := queue.acquire(ctx, describeAccountQueueKey(queueKey))
	if err != nil {
		logger.Warn(fmt.Sprintf("[account-queue] canceled account=%s wait=%s", describeAccountQueueKey(queueKey), time.Since(waitStartedAt).Truncate(time.Millisecond)))
		sharedAccountQueues.release(queueKey, queue)
		return nil, err
	}
	logger.Info(fmt.Sprintf("[account-queue] acquired account=%s waited=%t wait=%s pending=%d", describeAccountQueueKey(queueKey), waited, time.Since(waitStartedAt).Truncate(time.Millisecond), queue.pendingCount()))

	var once sync.Once
	return func() {
		once.Do(func() {
			pending := queue.pendingCount()
			queue.release()
			logger.Info(fmt.Sprintf("[account-queue] released account=%s pending=%d", describeAccountQueueKey(queueKey), pending))
			sharedAccountQueues.release(queueKey, queue)
		})
	}, nil
}

func (r *accountQueueRegistry) retain(queueKey string) *accountRequestQueue {
	r.mu.Lock()
	defer r.mu.Unlock()

	queue, ok := r.queues[queueKey]
	if !ok {
		queue = &accountRequestQueue{}
		r.queues[queueKey] = queue
	}
	queue.refs++
	return queue
}

func (r *accountQueueRegistry) release(queueKey string, queue *accountRequestQueue) {
	r.mu.Lock()
	defer r.mu.Unlock()

	current, ok := r.queues[queueKey]
	if !ok || current != queue {
		return
	}

	queue.refs--
	if queue.refs == 0 {
		delete(r.queues, queueKey)
	}
}

func (q *accountRequestQueue) acquire(ctx context.Context, accountLabel string) (bool, error) {
	q.mu.Lock()
	if !q.active {
		q.active = true
		q.mu.Unlock()
		return false, nil
	}

	waitCh := make(chan struct{})
	q.waiters = append(q.waiters, waitCh)
	pending := len(q.waiters)
	q.mu.Unlock()
	logger.Info(fmt.Sprintf("[account-queue] queued account=%s pending=%d", accountLabel, pending))

	select {
	case <-waitCh:
		return true, nil
	case <-ctx.Done():
		q.mu.Lock()
		idx := -1
		for i, candidate := range q.waiters {
			if candidate == waitCh {
				idx = i
				break
			}
		}
		if idx >= 0 {
			q.waiters = append(q.waiters[:idx], q.waiters[idx+1:]...)
			q.mu.Unlock()
			return true, ctx.Err()
		}
		q.mu.Unlock()

		// The waiter was already promoted while the context was canceled.
		<-waitCh
		q.release()
		return true, ctx.Err()
	}
}

func (q *accountRequestQueue) release() {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.waiters) == 0 {
		q.active = false
		return
	}

	next := q.waiters[0]
	q.waiters = q.waiters[1:]
	close(next)
}

func (q *accountRequestQueue) pendingCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.waiters)
}

func accountQueueKey(accessToken string) string {
	token := normalizeAccountToken(accessToken)
	if token == "" {
		return ""
	}

	if claims, ok := parseAccountQueueClaims(token); ok {
		switch {
		case claims.Auth.UserID != "":
			return "user_id:" + claims.Auth.UserID
		case claims.Sub != "":
			return "sub:" + claims.Sub
		case claims.Profile.Email != "":
			return "email:" + strings.ToLower(strings.TrimSpace(claims.Profile.Email))
		}
	}

	sum := sha256.Sum256([]byte(token))
	return "sha256:" + hex.EncodeToString(sum[:16])
}

func normalizeAccountToken(accessToken string) string {
	normalized := strings.TrimSpace(accessToken)
	if normalized == "" {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(normalized, "Bearer "))
}

func parseAccountQueueClaims(accessToken string) (*accountQueueClaims, bool) {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return nil, false
	}

	payload, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, false
	}

	var claims accountQueueClaims
	if err = json.Unmarshal(payload, &claims); err != nil {
		return nil, false
	}
	return &claims, true
}

func describeAccountQueueKey(queueKey string) string {
	keyType := "token"
	if idx := strings.Index(queueKey, ":"); idx >= 0 {
		keyType = queueKey[:idx]
	}
	sum := sha256.Sum256([]byte(queueKey))
	return fmt.Sprintf("%s#%s", keyType, hex.EncodeToString(sum[:4]))
}
