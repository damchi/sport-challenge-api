package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

// peer is the address httptest.NewRequest gives to every request.
const peer = "192.0.2.1"

func newFakeClock() *clock.FakeClock {
	return clock.NewFake(time.Date(2026, 3, 4, 15, 0, 0, 0, time.UTC))
}

// newIPLimitedRouter serves GET /items behind RateLimitIP, trusting proxies.
func newIPLimitedRouter(t *testing.T, limiter *RateLimiter, proxies ...string) *gin.Engine {
	t.Helper()
	r := NewRouter(discard())
	if err := r.SetTrustedProxies(proxies); err != nil {
		t.Fatal(err)
	}
	r.Use(RateLimitIP(limiter, discard()))
	r.GET("/items", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	return r
}

// header is a request header of get.
type header struct{ name, value string }

// get sends GET /items from remoteAddr (the httptest peer when empty) with
// the given headers.
func get(r http.Handler, remoteAddr string, headers ...header) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/items", nil)
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	for _, h := range headers {
		req.Header.Add(h.name, h.value)
	}
	return serve(r, req)
}

func TestRateLimitIP(t *testing.T) {
	clk := newFakeClock()
	r := newIPLimitedRouter(t, NewRateLimiter(1, 2, clk))

	for i := range 2 {
		if rec := get(r, ""); rec.Code != http.StatusNoContent {
			t.Fatalf("request %d within the burst got %d, want 204", i+1, rec.Code)
		}
	}
	rec := get(r, "")
	if rec.Code != http.StatusTooManyRequests || decodeError(t, rec).Code != "rate_limited" {
		t.Fatalf("beyond the burst got %d %s, want 429 rate_limited", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1", got)
	}
	if decodeError(t, rec).RequestID == "" {
		t.Fatal("429 without request identifier")
	}

	// Another address has its own bucket.
	if rec := get(r, "192.0.2.2:1234"); rec.Code != http.StatusNoContent {
		t.Fatalf("another IP got %d, want 204", rec.Code)
	}

	// Refused requests take no token: one second gives one request back.
	for range 5 {
		get(r, "")
	}
	clk.Advance(time.Second)
	if rec := get(r, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("after one second got %d, want 204", rec.Code)
	}
	if rec := get(r, ""); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request after one second got %d, want 429", rec.Code)
	}
}

func TestRateLimitRetryAfterIsRoundedUp(t *testing.T) {
	tests := []struct {
		name    string
		rps     float64
		elapsed time.Duration
		want    string
	}{
		{name: "one token every two seconds", rps: 0.5, want: "2"},
		{name: "part of a second left", rps: 0.5, elapsed: 500 * time.Millisecond, want: "2"},
		{name: "never zero", rps: 100, want: "1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clk := newFakeClock()
			r := newIPLimitedRouter(t, NewRateLimiter(tt.rps, 1, clk))
			get(r, "")
			clk.Advance(tt.elapsed)
			rec := get(r, "")
			if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != tt.want {
				t.Fatalf("got %d, Retry-After %q, want 429 and %s", rec.Code, rec.Header().Get("Retry-After"), tt.want)
			}
		})
	}
}

// TestRateLimitIPIgnoresForgedHeaders: a client cannot leave its bucket by
// writing the headers a proxy would set.
func TestRateLimitIPIgnoresForgedHeaders(t *testing.T) {
	tests := []struct {
		name    string
		proxies []string
		// first and second are the headers of two requests of the same
		// client, which must share one bucket.
		first, second []header
	}{
		{
			name:   "no trusted proxy: X-Forwarded-For is not read",
			first:  []header{{"X-Forwarded-For", "203.0.113.1"}},
			second: []header{{"X-Forwarded-For", "203.0.113.2"}},
		},
		{
			name:    "untrusted peer: X-Forwarded-For is not read",
			proxies: []string{"10.0.0.0/8"},
			first:   []header{{"X-Forwarded-For", "203.0.113.1"}},
			second:  []header{{"X-Forwarded-For", "203.0.113.2"}},
		},
		{
			name:    "trusted proxy: only the entry it appended counts",
			proxies: []string{peer},
			first:   []header{{"X-Forwarded-For", "203.0.113.1, 198.51.100.7"}},
			second:  []header{{"X-Forwarded-For", "203.0.113.2, 198.51.100.7"}},
		},
		{
			name:    "trusted proxy: a forged entry naming the proxy is skipped",
			proxies: []string{peer},
			first:   []header{{"X-Forwarded-For", "198.51.100.7"}},
			second:  []header{{"X-Forwarded-For", "203.0.113.2, " + peer + ", 198.51.100.7"}},
		},
		{
			name:    "trusted proxy: forged header sent apart",
			proxies: []string{peer},
			first:   []header{{"X-Forwarded-For", "198.51.100.7"}},
			second:  []header{{"X-Forwarded-For", "203.0.113.2"}, {"X-Forwarded-For", "198.51.100.7"}},
		},
		{
			name:    "trusted proxy: X-Real-IP is never read",
			proxies: []string{peer},
			first:   []header{{"X-Real-IP", "203.0.113.1"}},
			second:  []header{{"X-Real-IP", "203.0.113.2"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newIPLimitedRouter(t, NewRateLimiter(1, 1, newFakeClock()), tt.proxies...)
			if rec := get(r, "", tt.first...); rec.Code != http.StatusNoContent {
				t.Fatalf("first request got %d, want 204", rec.Code)
			}
			if rec := get(r, "", tt.second...); rec.Code != http.StatusTooManyRequests {
				t.Fatalf("second request got %d, want 429: the forged header gave a new bucket", rec.Code)
			}
		})
	}
}

// TestRateLimitIPBehindATrustedProxy: two clients behind the proxy do not
// share the bucket of the proxy.
func TestRateLimitIPBehindATrustedProxy(t *testing.T) {
	r := newIPLimitedRouter(t, NewRateLimiter(1, 1, newFakeClock()), peer)
	for _, client := range []string{"198.51.100.7", "198.51.100.8"} {
		if rec := get(r, "", header{"X-Forwarded-For", client}); rec.Code != http.StatusNoContent {
			t.Fatalf("client %s got %d, want 204", client, rec.Code)
		}
	}
	if rec := get(r, "", header{"X-Forwarded-For", "198.51.100.7"}); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request of a client got %d, want 429", rec.Code)
	}
}

func TestIPKey(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want string
	}{
		{name: "IPv4", ip: "198.51.100.7", want: "198.51.100.7"},
		{name: "IPv4 mapped in IPv6", ip: "::ffff:198.51.100.7", want: "198.51.100.7"},
		{name: "IPv6 is limited by /64", ip: "2001:db8:1:2:aaaa:bbbb:cccc:dddd", want: "2001:db8:1:2::/64"},
		{name: "same /64", ip: "2001:db8:1:2::1", want: "2001:db8:1:2::/64"},
		{name: "another /64", ip: "2001:db8:1:3::1", want: "2001:db8:1:3::/64"},
		{name: "IPv6 with a zone", ip: "fe80::1%eth0", want: "fe80::/64"},
		{name: "unreadable", ip: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ipKey(tt.ip); got != tt.want {
				t.Fatalf("ipKey(%q) = %q, want %q", tt.ip, got, tt.want)
			}
		})
	}
}

func TestRateLimiterDropsIdleBuckets(t *testing.T) {
	clk := newFakeClock()
	// The bucket refills in 2 seconds, but stays at least minBucketIdle.
	l := NewRateLimiter(1, 2, clk)
	l.allow("a")
	l.allow("a")
	clk.Advance(minBucketIdle - time.Second)
	l.allow("b")
	if len(l.buckets) != 2 {
		t.Fatalf("%d buckets before the sweep, want 2", len(l.buckets))
	}

	clk.Advance(time.Second)
	l.allow("c")
	if _, kept := l.buckets["a"]; kept || len(l.buckets) != 2 {
		t.Fatalf("buckets after the sweep: %d, a kept: %v; want b and c only", len(l.buckets), kept)
	}
	// Dropped once full again: the key gets its whole burst.
	for i := range 2 {
		if ok, _ := l.allow("a"); !ok {
			t.Fatalf("request %d of a dropped key refused", i+1)
		}
	}
}

// TestRateLimiterKeepsSlowBuckets: a bucket that takes longer than
// minBucketIdle to refill is kept until it is full, so sweeping never gives
// tokens back early.
func TestRateLimiterKeepsSlowBuckets(t *testing.T) {
	clk := newFakeClock()
	// One token every 2 minutes.
	l := NewRateLimiter(1.0/120, 1, clk)
	l.allow("a")
	clk.Advance(minBucketIdle + time.Second)
	l.allow("b")
	if ok, _ := l.allow("a"); ok {
		t.Fatal("request allowed before the bucket refilled")
	}
}

func TestRateLimitUser(t *testing.T) {
	type userKey struct{}
	userID := func(ctx context.Context) (string, bool) {
		id, ok := ctx.Value(userKey{}).(string)
		return id, ok
	}
	var served int
	newRouter := func() *gin.Engine {
		served = 0
		r := NewRouter(discard())
		// Stands for CurrentUser: the user comes from a test header.
		r.Use(func(c *gin.Context) {
			if id := c.GetHeader("X-Test-User"); id != "" {
				c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), userKey{}, id))
			}
		})
		r.Use(RateLimitUser(NewRateLimiter(1, 1, newFakeClock()), userID, discard()))
		r.Any("/items", func(c *gin.Context) {
			served++
			c.Status(http.StatusNoContent)
		})
		return r
	}
	do := func(r http.Handler, method, user string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/items", nil)
		if user != "" {
			req.Header.Set("X-Test-User", user)
		}
		return serve(r, req)
	}

	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete, http.MethodPut} {
		t.Run(method+" is limited by user", func(t *testing.T) {
			r := newRouter()
			if rec := do(r, method, "u1"); rec.Code != http.StatusNoContent {
				t.Fatalf("first write got %d, want 204", rec.Code)
			}
			rec := do(r, method, "u1")
			if rec.Code != http.StatusTooManyRequests || decodeError(t, rec).Code != "rate_limited" || rec.Header().Get("Retry-After") != "1" {
				t.Fatalf("second write got %d %s, Retry-After %q, want 429 rate_limited after 1 s",
					rec.Code, rec.Body.String(), rec.Header().Get("Retry-After"))
			}
			// Another user is not affected.
			if rec := do(r, method, "u2"); rec.Code != http.StatusNoContent {
				t.Fatalf("write of another user got %d, want 204", rec.Code)
			}
			if served != 2 {
				t.Fatalf("handler served %d requests, want 2", served)
			}
		})
	}

	t.Run("writes share one bucket whatever the method", func(t *testing.T) {
		r := newRouter()
		do(r, http.MethodPost, "u1")
		if rec := do(r, http.MethodDelete, "u1"); rec.Code != http.StatusTooManyRequests {
			t.Fatalf("DELETE after POST got %d, want 429", rec.Code)
		}
	})

	t.Run("reads are not limited", func(t *testing.T) {
		r := newRouter()
		do(r, http.MethodPost, "u1")
		for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
			for range 3 {
				if rec := do(r, method, "u1"); rec.Code != http.StatusNoContent {
					t.Fatalf("%s got %d, want 204", method, rec.Code)
				}
			}
		}
	})

	t.Run("a write without user is refused", func(t *testing.T) {
		r := newRouter()
		rec := do(r, http.MethodPost, "")
		if rec.Code != http.StatusUnauthorized || decodeError(t, rec).Code != "unauthenticated" || served != 0 {
			t.Fatalf("got %d %s, served %d, want 401 unauthenticated", rec.Code, rec.Body.String(), served)
		}
	})
}
