package cloudflare

import (
	"context"
	"errors"
	"io"
	"sync"
)

const DefaultMaxBytes int64 = 256 << 20

var ErrResponseBudget = errors.New("API 响应正文累计超过 --max-bytes；请缩小查询或提高额度")

type budgetKey struct{}
type responseBudget struct {
	mu        sync.Mutex
	remaining int64
	cancel    context.CancelCauseFunc
}

// WithResponseBudget shares decoded HTTP body bytes across all calls using ctx.
// Child timeouts can be replaced without replenishing the command's allowance.
// A nonpositive limit selects DefaultMaxBytes. Always call the returned cleanup.
func WithResponseBudget(ctx context.Context, limit int64) (context.Context, context.CancelFunc) {
	if limit <= 0 {
		limit = DefaultMaxBytes
	}
	ctx, cancel := context.WithCancelCause(ctx)
	budget := &responseBudget{remaining: limit, cancel: cancel}
	return context.WithValue(ctx, budgetKey{}, budget), func() { cancel(nil) }
}

func (b *responseBudget) available() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.remaining > 0
}

type budgetReader struct {
	reader io.Reader
	budget *responseBudget
}

func (r budgetReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	b := r.budget
	// Command requests are sequential. Serializing reads also keeps an explicitly
	// shared budget exact for concurrent callers, without speculative reservations.
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.remaining < 0 {
		return 0, ErrResponseBudget
	}
	if b.remaining < int64(len(p))-1 {
		p = p[:b.remaining+1]
	}
	n, err := r.reader.Read(p)
	b.remaining -= int64(n)
	if b.remaining < 0 {
		b.cancel(ErrResponseBudget)
		return n, ErrResponseBudget
	}
	return n, err
}
