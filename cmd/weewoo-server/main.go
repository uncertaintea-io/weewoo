package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
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

type serviceResponse struct {
	ID              int    `json:"id"`
	Name            string `json:"name"`
	PrometheusURL   string `json:"prometheusUrl"`
	LoadQuery       string `json:"loadQuery"`
	LatencyQuery    string `json:"latencyQuery"`
	IntervalSeconds int64  `json:"intervalSeconds"`
}

type createServiceRequest struct {
	Name            string     `json:"name"`
	PrometheusURL   string     `json:"prometheusUrl"`
	LoadQuery       string     `json:"loadQuery"`
	LatencyQuery    string     `json:"latencyQuery"`
	IntervalSeconds int64      `json:"intervalSeconds"`
	ImportStart     *time.Time `json:"importStart,omitempty"`
	ImportEnd       *time.Time `json:"importEnd,omitempty"`
}

type serviceCollector interface {
	Schedule(service *config.Service)
	Import(ctx context.Context, service *config.Service, start, end time.Time) error
}

func newServiceResponse(service *config.Service) serviceResponse {
	return serviceResponse{
		ID:              service.Id,
		Name:            service.Name,
		PrometheusURL:   service.PrometheusURL,
		LoadQuery:       service.LoadQuery,
		LatencyQuery:    service.LatencyQuery,
		IntervalSeconds: int64(service.Interval / time.Second),
	}
}

func NewListAllServicesHandler(cfg config.Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		services, err := cfg.ReadAllServices()
		if err != nil {
			http.Error(w, "failed to read services", http.StatusInternalServerError)
			return
		}

		response := make([]serviceResponse, 0, len(services))
		for _, service := range services {
			response = append(response, newServiceResponse(service))
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			log.Printf("failed to encode services response: %v", err)
		}
	})
}

func validateCreateService(request createServiceRequest) error {
	if request.Name == "" || request.PrometheusURL == "" || request.LoadQuery == "" || request.LatencyQuery == "" {
		return fmt.Errorf("name, prometheusUrl, loadQuery, and latencyQuery are required")
	}
	parsedURL, err := url.ParseRequestURI(request.PrometheusURL)
	if err != nil || parsedURL.Host == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return fmt.Errorf("prometheusUrl must be an HTTP or HTTPS URL")
	}
	if request.IntervalSeconds <= 0 {
		return fmt.Errorf("intervalSeconds must be greater than zero")
	}
	if (request.ImportStart == nil) != (request.ImportEnd == nil) {
		return fmt.Errorf("importStart and importEnd must be provided together")
	}
	if request.ImportStart != nil && !request.ImportStart.Before(*request.ImportEnd) {
		return fmt.Errorf("importStart must be before importEnd")
	}
	return nil
}

func NewCreateServiceHandler(cfg config.Config, collector serviceCollector) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var request createServiceRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			http.Error(w, "invalid JSON request", http.StatusBadRequest)
			return
		}
		if err := validateCreateService(request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		service := &config.Service{
			Name: request.Name, PrometheusURL: request.PrometheusURL,
			LoadQuery: request.LoadQuery, LatencyQuery: request.LatencyQuery,
			Interval: time.Duration(request.IntervalSeconds) * time.Second,
		}
		if err := cfg.WriteService(service); err != nil {
			http.Error(w, "failed to create service", http.StatusInternalServerError)
			return
		}
		collector.Schedule(service)
		if request.ImportStart != nil {
			if err := collector.Import(r.Context(), service, *request.ImportStart, *request.ImportEnd); err != nil {
				log.Printf("historical import failed for service %d: %v", service.Id, err)
				http.Error(w, "service created, but historical import failed", http.StatusBadGateway)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Location", fmt.Sprintf("/api/services/%d", service.Id))
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(newServiceResponse(service)); err != nil {
			log.Printf("failed to encode created service: %v", err)
		}
	})
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

	cfg := config.NewDatabaseConfig(db)
	defer cfg.Close()

	services, err := cfg.ReadAllServices()
	if err != nil {
		log.Fatalf("Failed to read services: %v", err)
	}
	scheduler := collection.NewIntervalScheduler(collection.WithSchedulerEventHandler(nil))
	defer scheduler.Stop()
	collector := collection.NewCollector(http.DefaultClient, ecdf.NewDatabaseChunkStore(db), scheduler)
	defer collector.Stop()
	for _, service := range services {
		collector.Schedule(service)
	}

	// Start ECDF builder
	err = collection.StartECDFBuilder(ecdf.NewDatabaseChunkStore(db), ecdf.NewDatabaseJointStore(db), cfg, scheduler)
	if err != nil {
		log.Fatalf("Failed to start ECDF builder: %v", err)
	}

	appMux := http.NewServeMux()
	appMux.Handle("/api/services", observeRequestDuration(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			NewCreateServiceHandler(cfg, collector).ServeHTTP(w, r)
			return
		}
		NewListAllServicesHandler(cfg).ServeHTTP(w, r)
	})))
	//Serve files from static folder
	appMux.Handle("/", observeRequestDuration(http.FileServer(http.Dir("./ui/dist"))))

	monitorPort := ":5000"
	appPort := ":8080"
	fmt.Println("Server is running on port" + appPort)
	fmt.Println("Metrics are running on port" + monitorPort)

	appServer := &http.Server{
		Addr:           appPort,
		Handler:        appMux,
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
