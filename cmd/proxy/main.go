// Program proxy is a naive http proxy implementation that limits in-flight requests to origin server.
package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/http/httputil"
	_ "net/http/pprof"
	"net/url"
	"runtime"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/time/rate"
)

func main() {
	originAddr := flag.String("origin", "http://localhost:8000", "origin address where to proxy requests")
	addr := flag.String("addr", ":7000", "address to listen to")
	quota := flag.Int64("quota", 5, "allowed number of concurrent requests")
	adaptive := flag.Bool("adaptive", false, "adaptive capacity control")
	flag.Parse()

	runtime.SetMutexProfileFraction(5)

	inflightRequests := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "proxy_inflight_requests",
		Help: "How many HTTP requests are in-flight.",
	})
	targetInflightRequests := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "proxy_target_inflight_requests",
		Help: "How many HTTP requests should be in-flight.",
	})
	prometheus.MustRegister(inflightRequests)
	prometheus.MustRegister(targetInflightRequests)
	http.Handle("/metrics", promhttp.Handler())

	inflight := NewQuota(*quota, inflightRequests, targetInflightRequests)
	// incLimiter throttles additive increase which happens on every HTTP 200 OK response.
	incLimiter := rate.NewLimiter(rate.Limit(1), 1)

	target, err := url.Parse(*originAddr)
	if err != nil {
		log.Fatalf("proxy: failed to parse origin url: %v", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ModifyResponse = func(resp *http.Response) error {
		if !*adaptive {
			return nil
		}

		if resp.StatusCode != http.StatusOK {
			inflight.Backoff(0.75)
			return nil
		}
		// Increase target concurrency by a constant c per unit time,
		// e.g., allow 1 more rps every second if there is a demand.
		if incLimiter.Allow() {
			inflight.Inc()
		}
		return nil
	}
	proxy.ErrorHandler = func(rw http.ResponseWriter, r *http.Request, err error) {
		log.Printf("proxy: %v", err)
		rw.WriteHeader(http.StatusBadGateway)
		if *adaptive {
			inflight.Backoff(0.75)
		}
	}

	http.HandleFunc("/", func(rw http.ResponseWriter, r *http.Request) {
		if inflight.Receive() {
			proxy.ServeHTTP(rw, r)
			inflight.Release()
			return
		}

		rw.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(rw, "🚦\n")
	})
	http.ListenAndServe(*addr, nil)
}

// Quota is a limited quantity of requests allowed to be in-flight.
type Quota struct {
	used int64
	max  int64

	current prometheus.Gauge
	target  prometheus.Gauge
}

// NewQuota creates a quota of in-flight requests.
func NewQuota(n int64, current, target prometheus.Gauge) *Quota {
	q := Quota{
		max:     n,
		current: current,
		target:  target,
	}
	return &q
}

// Receive fills quota by one and returns true if quota is available.
func (q *Quota) Receive() bool {
	used := atomic.LoadInt64(&q.used)
	max := atomic.LoadInt64(&q.max)
	available := used < max
	// If quota became available here, it's still ok to reject the request.
	if !available {
		return false
	}

	atomic.AddInt64(&q.used, 1)
	q.current.Inc()

	// If quota became unavailable here, it's still ok to process the request.
	return true
}

// Release frees up quota by one.
func (q *Quota) Release() {
	atomic.AddInt64(&q.used, -1)

	q.current.Dec()
}

// Inc lifts quota by one.
func (q *Quota) Inc() {
	atomic.AddInt64(&q.max, 1)

	q.target.Inc()
}

// Backoff sets target concurrency to a fraction p of its current size (0 <= p <= 1), e.g.,
// back-off to 75% when a service is overloaded.
func (q *Quota) Backoff(p float64) {
	for {
		oldMax := atomic.LoadInt64(&q.max)
		newMax := math.Ceil(p * float64(oldMax))
		if atomic.CompareAndSwapInt64(&q.max, oldMax, int64(newMax)) {
			q.target.Set(newMax)
			break
		}
	}
}
