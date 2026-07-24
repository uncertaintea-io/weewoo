package alerting

// This package is using prometheus alertmanager to send alerts on port 9093
// It will be used to send alerts to the user when the system is not working as expected.

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"time"

	"github.com/go-openapi/strfmt"
	amclient "github.com/prometheus/alertmanager/api/v2/client"
	"github.com/prometheus/alertmanager/api/v2/client/alert"
	"github.com/prometheus/alertmanager/api/v2/models"
	"github.com/uncertaintea-io/weewoo/internal/config"
)

type AlertingOptions struct {
	Service          string
	Serverity        string
	Indicator        string
	AlertName        string
	Summary          string
	Description      string
	Impact           string
	SuggestedAction  string
	TechnicalDetails string
	Annotations      map[string]string
	Labels           map[string]string
	GeneratorURL     string
	StartsAt         time.Time
	EndsAt           time.Time
}

func SendIt(cfg config.Config, options AlertingOptions) error {
	return SendItContext(context.Background(), cfg, options)
}

func SendItContext(ctx context.Context, cfg config.Config, options AlertingOptions) error {
	alertmanagerHost, err := cfg.GetConfig("alertmanager_host")
	if err != nil {
		return fmt.Errorf("failed to get alertmanager host: %w", err)
	}
	return sendToAlertmanagerHost(ctx, alertmanagerHost, options)
}

func sendToAlertmanagerHost(ctx context.Context, alertmanagerHost string, options AlertingOptions) error {
	// configure the transport to use the alertmanager API
	transportConfig := amclient.DefaultTransportConfig().WithHost(alertmanagerHost)
	api := amclient.NewHTTPClientWithConfig(strfmt.Default, transportConfig)

	slog.Debug("sending alert to alertmanager", "host", alertmanagerHost)

	annotations := models.LabelSet{
		"description": options.Description,
		"summary":     options.Summary,
	}
	if options.Impact != "" {
		annotations["impact"] = options.Impact
	}
	if options.SuggestedAction != "" {
		annotations["suggested_action"] = options.SuggestedAction
	}
	if options.TechnicalDetails != "" {
		annotations["technical_details"] = options.TechnicalDetails
	}
	if options.Annotations != nil {
		maps.Copy(annotations, options.Annotations)
	}
	labels := models.LabelSet{
		"alertname": options.AlertName,
		"severity":  options.Serverity,
		"instance":  options.GeneratorURL,
		"service":   options.Service,
		"indicator": options.Indicator,
	}
	if options.Labels != nil {
		maps.Copy(labels, options.Labels)
	}
	startsAt := options.StartsAt
	if startsAt.IsZero() {
		startsAt = time.Now()
	}

	// build the alert payload
	alertPayload := models.PostableAlerts{
		&models.PostableAlert{
			StartsAt: strfmt.DateTime(startsAt),
			EndsAt:   strfmt.DateTime(options.EndsAt),
			Alert: models.Alert{
				GeneratorURL: strfmt.URI(options.GeneratorURL),
				Labels:       labels,
			},
			Annotations: annotations,
		},
	}
	// prepare the request parameters
	params := alert.NewPostAlertsParams().WithContext(ctx).WithHTTPClient(http.DefaultClient).WithAlerts(alertPayload)

	slog.Debug("params", "params", params)
	// send the alerts over HTTP v2 API
	_, err := api.Alert.PostAlerts(params)
	if err != nil {
		return fmt.Errorf("failed to send alert: %w", err)
	}

	// print the response
	slog.Debug("alert sent successfully")
	return nil
}
