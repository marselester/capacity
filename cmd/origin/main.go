package main

import (
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type job struct {
	result chan struct{}
}

func main() {
	addr := flag.String("addr", ":8000", "address to listen to")
	workerNum := flag.Int("worker", 7, "number of workers to process requests")
	worktime := flag.Duration("worktime", time.Second, "how long it takes to process a request")
	queueSize := flag.Int("queue", 0, "how many requests to keep in a queue if workers are busy")
	flag.Parse()

	requestTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "origin_requests_total",
			Help: "How many HTTP requests processed, partitioned by status code.",
		},
		[]string{"status"},
	)
	requestLatency := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "origin_request_duration_seconds",
		Help:    "Total duration of HTTP requests in seconds.",
		Buckets: []float64{0.95, 1, 1.05, 1.1, 1.5, 1.95, 2, 2.05, 2.1, 2.5, 3, 4},
	})
	prometheus.MustRegister(requestLatency)
	prometheus.MustRegister(requestTotal)
	http.Handle("/metrics", promhttp.Handler())

	// Initialize the default source of uniformly-distributed pseudo-random ints.
	rand.Seed(time.Now().UnixNano())

	jobs := make(chan job, *queueSize)

	http.HandleFunc("/", func(rw http.ResponseWriter, r *http.Request) {
		var status int
		defer func(begun time.Time) {
			took := time.Since(begun)
			requestLatency.Observe(took.Seconds())
			fmt.Printf("request took %v\n", took)

			requestTotal.With(prometheus.Labels{
				"status": fmt.Sprint(status),
			}).Inc()
		}(time.Now())

		j := job{
			result: make(chan struct{}),
		}
		select {
		case jobs <- j:
			<-j.result
			status = http.StatusOK
			rw.WriteHeader(status)
			fmt.Fprint(rw, "🐈\n")
		// Discard requests if workers are busy and queue is full.
		default:
			status = http.StatusTooManyRequests
			rw.WriteHeader(status)
			fmt.Fprint(rw, "🚦\n")
		}
	})
	go http.ListenAndServe(*addr, nil)

	fmt.Printf("starting %d workers\n", *workerNum)
	var wg sync.WaitGroup
	for i := 0; i < *workerNum; i++ {
		wg.Add(1)
		go func(workerID int) {
			for j := range jobs {
				begun := time.Now()
				time.Sleep(randDuration(*worktime))
				j.result <- struct{}{}
				fmt.Printf("worker #%d completed job in %v, %d left\n", workerID, time.Since(begun), len(jobs))
			}
			wg.Done()
		}(i)
	}
	wg.Wait()
}

func randDuration(mean time.Duration) time.Duration {
	return time.Duration(rand.NormFloat64() + float64(mean))
}
