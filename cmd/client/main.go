package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/time/rate"
)

func main() {
	originAddr := flag.String("origin", "http://localhost:8000", "origin address where to send requests")
	addr := flag.String("addr", ":8080", "address to expose metrics at")
	workerNum := flag.Int("worker", 10, "number of workers to generate load")
	rps := flag.Float64("rps", 5, "requests allowed to send per second")
	timeout := flag.Duration("timeout", 2500*time.Millisecond, "how long to wait for a response")
	flag.Parse()

	requestTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "client_requests_total",
			Help: "How many HTTP requests processed, partitioned by status code.",
		},
		[]string{"status"},
	)
	requestLatency := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "client_request_duration_seconds",
		Help:    "Total duration of HTTP requests in seconds.",
		Buckets: []float64{0.95, 1, 1.05, 1.1, 1.5, 1.95, 2, 2.05, 2.1, 2.5},
	})
	prometheus.MustRegister(requestLatency)
	prometheus.MustRegister(requestTotal)
	http.Handle("/metrics", promhttp.Handler())
	go http.ListenAndServe(*addr, nil)

	// limiter throttles requests that exceeded rps requests per second.
	limiter := rate.NewLimiter(rate.Limit(*rps), int(*rps))

	ctx := context.Background()

	fmt.Printf("starting %d workers\n", *workerNum)
	var wg sync.WaitGroup
	for i := 0; i < *workerNum; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for {
				if err := limiter.Wait(ctx); err != nil {
					if ctx.Err() != nil {
						return
					}
				}

				ctx, cancel := context.WithTimeout(ctx, *timeout)
				err := fetch(ctx, *originAddr, requestTotal, requestLatency)
				cancel()
				if err != nil {
					fmt.Printf("worker #%d: %v\n", workerID, err)
					continue
				}
				fmt.Printf("worker #%d: ok\n", workerID)
			}
		}(i)
	}
	wg.Wait()
}

func fetch(ctx context.Context, addr string, total *prometheus.CounterVec, latency prometheus.Histogram) error {
	var status int

	defer func(begun time.Time) {
		latency.Observe(time.Since(begun).Seconds())
		total.With(prometheus.Labels{
			"status": fmt.Sprint(status),
		}).Inc()
	}(time.Now())

	req, err := http.NewRequest(http.MethodGet, addr, nil)
	if err != nil {
		status = http.StatusBadGateway
		return err
	}
	req = req.WithContext(ctx)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		status = http.StatusBadGateway
		return err
	}
	resp.Body.Close()

	status = resp.StatusCode
	return nil
}
