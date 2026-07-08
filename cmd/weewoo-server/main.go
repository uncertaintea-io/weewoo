package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/uncertaintea-io/weewoo/internal/collection"
	"github.com/uncertaintea-io/weewoo/internal/config"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

func NewMetricshandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	return mux
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.status = code
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += n
	return n, err
}

// requestDuration tracks total app request latency in seconds.
var requestDuration = promauto.NewHistogramVec(
	prometheus.HistogramOpts{
		Namespace:                   "weewoo",
		Subsystem:                   "http",
		Name:                        "request_duration_seconds",
		Help:                        "HTTP request latency in seconds.",
		NativeHistogramBucketFactor: 1.1,
	},
	[]string{"status_code"},
)

// observeRequestDuration records how long the wrapped handler takes.
func observeRequestDuration(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}

		next.ServeHTTP(rec, r)

		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}

		requestDuration.WithLabelValues(strconv.Itoa(status)).Observe(time.Since(start).Seconds())
	})
}

// todo adding startecdf builder to the main function
func main() {
	configfile := flag.String("config", "config.yaml", "Config file")
	flag.Parse()
	systemSettings, err := config.ReadSystemSettings(*configfile)
	if err != nil {
		log.Fatalf("Failed to read system settings: %v", err)
	}
	db, err := systemSettings.OpenDatabase()
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	databaseConfig := config.NewDatabaseConfig(db)
	defer databaseConfig.Close()

	services, err := databaseConfig.ReadAllServices()
	if err != nil {
		log.Fatalf("Failed to read services: %v", err)
	}
	collector := collection.NewCollector(http.DefaultClient, ecdf.NewDatabaseChunkStore(db))
	defer collector.Stop()
	for _, service := range services {
		collector.Schedule(service)
	}

	// Start ECDF builder
	scheduler := collection.NewIntervalScheduler(collection.WithSchedulerEventHandler(nil))
	defer scheduler.Stop()
	err = collection.StartECDFBuilder(ecdf.NewDatabaseChunkStore(db), databaseConfig, scheduler)
	if err != nil {
		log.Fatalf("Failed to start ECDF builder: %v", err)
	}

	//Serve files from static folder
	http.Handle("/", observeRequestDuration(http.FileServer(http.Dir("./ui/dist"))))
	monitorPort := ":5000"
	appPort := ":8080"
	fmt.Println("Server is running on port" + appPort)
	fmt.Println("Metrics are running on port" + monitorPort)

	appServer := &http.Server{
		Addr:           appPort,
		Handler:        nil,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	metricsServer := &http.Server{
		Addr:    monitorPort,
		Handler: NewMetricshandler(),
	}

	serverErr := make(chan error)
	go func() {
		serverErr <- metricsServer.ListenAndServe()
	}()

	go func() {
		serverErr <- appServer.ListenAndServe()
	}()

	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case <-signalCtx.Done():

	case err := <-serverErr:
		log.Fatal(err)
	}

	appServer.Shutdown(context.Background())
	metricsServer.Shutdown(context.Background())
}
