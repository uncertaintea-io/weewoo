package alerting

// This package is using prometheus alertmanager to send alerts on port 9093
// It will be used to send alerts to the user when the system is not working as expected.

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-openapi/strfmt"
	amclient "github.com/prometheus/alertmanager/api/v2/client"
	"github.com/prometheus/alertmanager/api/v2/client/alert"
	"github.com/prometheus/alertmanager/api/v2/models"
	"github.com/uncertaintea-io/weewoo/internal/config"
)

type AlertingOptions struct {
	Service      string
	Serverity    string
	Indicator    string
	AlertName    string
	Summary      string
	Description  string
	Annotations  map[string]string
	GeneratorURL string
}

func SendIt(cfg config.Config, options AlertingOptions) error {
	// configure the transport to use the alertmanager API
	transportConfig := amclient.DefaultTransportConfig().WithHost(cfg.AlertManagerURL())
	api := amclient.NewHTTPClientWithConfig(strfmt.Default, transportConfig)

	slog.Debug("sending alert to alertmanager", "url", cfg.AlertManagerURL())
	// build the alert payload
	alertPayload := models.PostableAlerts{
		&models.PostableAlert{
			StartsAt: strfmt.DateTime(time.Now()),
			Alert: models.Alert{
				GeneratorURL: strfmt.URI(options.GeneratorURL),
				Labels: models.LabelSet{
					"alertname": options.AlertName,
					"severity":  options.Serverity,
					"instance":  options.GeneratorURL,
					"service":   options.Service,
					"indicator": options.Indicator,
				},
			},
			Annotations: models.LabelSet{
				"description": options.Description,
				"summary":     options.Summary,
			},
		},
	}
	// prepare the request parameters
	params := alert.NewPostAlertsParams().WithContext(context.Background()).WithHTTPClient(http.DefaultClient).WithAlerts(alertPayload)

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
