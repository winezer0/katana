package standard

import (
	"fmt"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/projectdiscovery/katana/pkg/output"
	"github.com/projectdiscovery/katana/pkg/types"
	"github.com/stretchr/testify/require"
)

// connTracker counts the server side connections that are still open.
type connTracker struct {
	mu   sync.Mutex
	open map[net.Conn]struct{}
}

func (c *connTracker) track(conn net.Conn, state http.ConnState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch state {
	case http.StateNew:
		c.open[conn] = struct{}{}
	case http.StateClosed, http.StateHijacked:
		delete(c.open, conn)
	}
}

func (c *connTracker) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.open)
}

func newTrackedServer(t *testing.T) (*httptest.Server, *connTracker) {
	t.Helper()
	tracker := &connTracker{open: make(map[net.Conn]struct{})}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = fmt.Fprint(w, "User-agent: *\nDisallow: /private\n")
		case "/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<html><body><a href="/next">next</a></body></html>`)
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<html><body>ok</body></html>`)
		}
	}))
	srv.Config.ConnState = tracker.track
	srv.Start()
	t.Cleanup(srv.Close)
	return srv, tracker
}

func newTestCrawler(t *testing.T, knownFiles string) *Crawler {
	t.Helper()
	options, err := types.NewCrawlerOptions(&types.Options{
		MaxDepth:     3,
		FieldScope:   "rdn",
		BodyReadSize: math.MaxInt,
		Timeout:      1,
		Retries:      1,
		Concurrency:  2,
		Parallelism:  2,
		RateLimit:    150,
		Strategy:     "breadth-first",
		KnownFiles:   knownFiles,
		Silent:       true,
		OnResult:     func(output.Result) {},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = options.Close() })

	crawler, err := New(options)
	require.NoError(t, err)
	return crawler
}

func TestCrawlFollowsSourceMapsOnlyWhenEnabled(t *testing.T) {
	testCases := []struct {
		name    string
		enabled bool
	}{
		{name: "enabled", enabled: true},
		{name: "disabled", enabled: false},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var (
				mu        sync.Mutex
				requested = make(map[string]int)
			)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requested[r.URL.Path]++
				mu.Unlock()

				switch r.URL.Path {
				case "/":
					w.Header().Set("Content-Type", "text/html")
					_, _ = fmt.Fprint(w, `<html><script src="/assets/app.js"></script></html>`)
				case "/assets/app.js":
					w.Header().Set("Content-Type", "application/javascript")
					_, _ = fmt.Fprint(w, "//# sourceMappingURL=app.js.map\n")
				case "/assets/app.js.map":
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprint(w, `{"version":3,"sources":["src/embedded.js","src/no-content.js"],"sourcesContent":["fetch('/api/from-map-content')",null]}`)
				case "/assets/src/no-content.js":
					w.Header().Set("Content-Type", "application/javascript")
					_, _ = fmt.Fprint(w, "fetch('/api/from-fetched-source')\n")
				default:
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprint(w, `{"ok":true}`)
				}
			}))
			t.Cleanup(server.Close)

			options, err := types.NewCrawlerOptions(&types.Options{
				MaxDepth:             5,
				FieldScope:           "rdn",
				BodyReadSize:         math.MaxInt,
				Timeout:              1,
				Retries:              1,
				Concurrency:          2,
				Parallelism:          2,
				RateLimit:            150,
				Strategy:             "breadth-first",
				ScrapeJSMapResponses: testCase.enabled,
				Silent:               true,
				OnResult:             func(output.Result) {},
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = options.Close() })

			crawler, err := New(options)
			require.NoError(t, err)
			t.Cleanup(func() { _ = crawler.Close() })

			require.NoError(t, crawler.Crawl(server.URL))

			mu.Lock()
			defer mu.Unlock()
			mapRequests := requested["/assets/app.js.map"]
			if testCase.enabled {
				require.Positive(t, mapRequests)
				require.Positive(t, requested["/assets/src/no-content.js"])
				require.Positive(t, requested["/api/from-map-content"])
				require.Positive(t, requested["/api/from-fetched-source"])
				return
			}
			require.Zero(t, mapRequests)
			require.Zero(t, requested["/assets/src/no-content.js"])
			require.Zero(t, requested["/api/from-map-content"])
			require.Zero(t, requested["/api/from-fetched-source"])
		})
	}
}

func requireNoOpenConnections(t *testing.T, tracker *connTracker) {
	t.Helper()
	require.Eventually(t, func() bool { return tracker.count() == 0 }, 2*time.Second, 20*time.Millisecond,
		"connections still open: %d", tracker.count())
}

func TestCrawlClosesIdleConnections(t *testing.T) {
	srv, tracker := newTrackedServer(t)
	crawler := newTestCrawler(t, "")
	t.Cleanup(func() { _ = crawler.Close() })

	for i := 0; i < 3; i++ {
		require.NoError(t, crawler.Crawl(srv.URL))
		requireNoOpenConnections(t, tracker)
	}
}

func TestCloseClosesKnownFilesIdleConnections(t *testing.T) {
	srv, tracker := newTrackedServer(t)
	crawler := newTestCrawler(t, "robotstxt")

	require.NoError(t, crawler.Crawl(srv.URL))
	require.NoError(t, crawler.Close())
	requireNoOpenConnections(t, tracker)
}
