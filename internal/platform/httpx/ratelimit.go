package httpx

import (
	"context"
	"log/slog"
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

// minBucketIdle is the shortest time a bucket stays in memory unused, so the
// buckets are not swept on every request when they refill in a few seconds.
const minBucketIdle = time.Minute

// RateLimiter holds one token bucket per key (an IP address, a user), in the
// memory of the instance: each Cloud Run instance counts on its own. A bucket
// starts full with burst tokens and gets rps tokens back per second.
//
// A bucket unused for the time it takes to refill is full again: dropping it
// loses nothing, and the memory follows the keys seen recently.
type RateLimiter struct {
	limit rate.Limit
	burst int
	clock clock.Clock
	// idle is how long a bucket stays unused before it is dropped, and the
	// interval between two sweeps.
	idle time.Duration

	mu      sync.Mutex
	buckets map[string]*bucket
	swept   time.Time
}

type bucket struct {
	limiter *rate.Limiter
	seen    time.Time
}

// NewRateLimiter returns a limiter of rps requests per second and per key,
// with bursts of burst requests. Both must be positive (the configuration
// checks them): a limiter without burst would refuse everything.
func NewRateLimiter(rps float64, burst int, clk clock.Clock) *RateLimiter {
	refill := time.Duration(float64(burst) / rps * float64(time.Second))
	return &RateLimiter{
		limit:   rate.Limit(rps),
		burst:   burst,
		clock:   clk,
		idle:    max(refill, minBucketIdle),
		buckets: make(map[string]*bucket),
		swept:   clk.Now(),
	}
}

// allow takes a token from the bucket of key. When the bucket is empty it
// takes nothing and returns how long until a token is back.
func (l *RateLimiter) allow(key string) (bool, time.Duration) {
	now := l.clock.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	if now.Sub(l.swept) >= l.idle {
		for k, b := range l.buckets {
			if now.Sub(b.seen) >= l.idle {
				delete(l.buckets, k)
			}
		}
		l.swept = now
	}

	b := l.buckets[key]
	if b == nil {
		b = &bucket{limiter: rate.NewLimiter(l.limit, l.burst)}
		l.buckets[key] = b
	}
	b.seen = now
	reservation := b.limiter.ReserveN(now, 1)
	if delay := reservation.DelayFrom(now); delay > 0 {
		// Give the token back: a refused request must not push the next
		// allowed one further away.
		reservation.CancelAt(now)
		return false, delay
	}
	return true, 0
}

// handle lets the request through if the bucket of key has a token, otherwise
// answers 429 rate_limited with Retry-After in whole seconds, rounded up.
func (l *RateLimiter) handle(c *gin.Context, logger *slog.Logger, key string) {
	ok, delay := l.allow(key)
	if ok {
		return
	}
	c.Header("Retry-After", strconv.Itoa(max(1, int(math.Ceil(delay.Seconds())))))
	RespondError(c, logger, apperr.New(apperr.CodeRateLimited, nil))
}

// RateLimitIP limits the requests of each client IP address, on every route.
// It sits before Authenticate, so a flood is refused before any token is
// verified. The address is c.ClientIP(): the peer address, or the entry of
// X-Forwarded-For added by the trusted proxies of the engine (never an entry
// the client wrote itself). It never logs the address.
func RateLimitIP(limiter *RateLimiter, logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		limiter.handle(c, logger, ipKey(c.ClientIP()))
	}
}

// ipKey is the bucket of an IP address: the address itself in IPv4, its /64
// in IPv6, where one subscriber gets a whole /64 and would otherwise have a
// bucket per address. An unreadable address shares a single bucket.
func ipKey(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	addr = addr.Unmap()
	if addr.Is4() {
		return addr.String()
	}
	// Bits is in range: Prefix cannot fail.
	prefix, _ := addr.WithZone("").Prefix(64)
	return prefix.String()
}

// RateLimitUser limits the write requests (every method but GET, HEAD and
// OPTIONS) of each user, on top of the limit by IP address. It runs after
// CurrentUser, which userID reads the user from (auth.UserID), and before
// the handler: a user beyond the limit gets 429 before any business answer.
// A request without user answers 401 unauthenticated. Like Authenticate, it
// never calls c.Next, so Protect can chain it.
func RateLimitUser(limiter *RateLimiter, userID func(context.Context) (string, bool), logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			return
		}
		id, ok := userID(c.Request.Context())
		if !ok {
			RespondError(c, logger, apperr.New(apperr.CodeUnauthenticated, nil))
			return
		}
		limiter.handle(c, logger, id)
	}
}
