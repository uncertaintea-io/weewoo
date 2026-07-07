package alerting

// This package is using prometheus alertmanager to send alerts on port 9093
// It will be used to send alerts to the user when the system is not working as expected.

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-openapi/strfmt"
	amclient "github.com/prometheus/alertmanager/api/v2/client"
	"github.com/prometheus/alertmanager/api/v2/client/alert"
	"github.com/prometheus/alertmanager/api/v2/models"
)

func SendIt() {
	// configure the transport to use the alertmanager API
	cfg := amclient.DefaultTransportConfig().WithHost("pc0:9093")
	api := amclient.NewHTTPClientWithConfig(strfmt.Default, cfg)

	fmt.Println("sending alert to alertmanager")
	fmt.Println("alertmanager URL: ", cfg.Host)
	// build the alert payload
	alertPayload := models.PostableAlerts{
		&models.PostableAlert{
			StartsAt: strfmt.DateTime(time.Now()),
			Alert: models.Alert{
				GeneratorURL: "http://localhost:9093/alerts",
				Labels: models.LabelSet{
					"alertname": "test",
					"severity":  "critical",
					"instance":  "pc0:9093",
				},
			},
		},
	}
	fmt.Println("alert payload: ", alertPayload)
	// prepare the request parameters
	params := alert.NewPostAlertsParams().WithContext(context.Background()).WithHTTPClient(http.DefaultClient).WithAlerts(alertPayload)

	fmt.Println("params: ", params)
	// send the alerts over HTTP v2 API
	_, err := api.Alert.PostAlerts(params)
	if err != nil {
		log.Fatalf("failed to send alert: %v", err)
	}

	// print the response
	fmt.Println("alert sent successfully")
}
