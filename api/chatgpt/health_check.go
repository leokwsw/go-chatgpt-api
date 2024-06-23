package chatgpt

import (
	"github.com/linweiyuan/go-logger/logger"
	"os"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/leokwsw/go-chatgpt-api/api"

	http "github.com/bogdanfinn/fhttp"
)

const (
	healthCheckUrl = ApiPrefix + "/accounts/check"
	readyHint      = "Service go-chatgpt-api is ready."
	errorHintBlock = "Looks like you have bean blocked -> curl https://chatgpt.com | grep '<p>' | awk '{$1=$1;print}'"
	errorHint403   = "Failed to handle 403."
	sleepHours     = 8760 // 365 days
)

func init() {
	resp, err := healthCheck()
	if err != nil {
		logger.Error("Health check failed: " + err.Error())
		os.Exit(1)
	}

	checkHealthCheckStatus(resp)
}

func healthCheck() (resp *http.Response, err error) {
	req, _ := http.NewRequest(http.MethodGet, healthCheckUrl, nil)
	req.Header.Set("User-Agent", api.UserAgent)
	resp, err = api.Client.Do(req)
	return
}

func checkHealthCheckStatus(resp *http.Response) {
	defer resp.Body.Close()
	if resp != nil && resp.StatusCode == http.StatusUnauthorized {
		logger.Info(readyHint)
	} else {
		doc, _ := goquery.NewDocumentFromReader(resp.Body)
		alert := doc.Find(".message").Text()
		if alert != "" {
			logger.Error(errorHintBlock)
		} else {
			logger.Error(errorHint403)
			logger.Warn(doc.Text())
		}
		time.Sleep(time.Hour * sleepHours)
		os.Exit(1)
	}
}
