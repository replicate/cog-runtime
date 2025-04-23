package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"
	"time"
)

type PendingPrediction struct {
	request     PredictionRequest
	response    PredictionResponse
	lastUpdated time.Time
	inputPaths  []string
	mu          sync.Mutex
	c           chan *PredictionResponse
}

func (pr *PendingPrediction) appendLogLine(line string) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	pr.response.Logs += fmt.Sprintln(line)
}

func (pr *PendingPrediction) sendWebhook(event WebhookEvent) error {
	pr.mu.Lock()
	defer pr.mu.Unlock()

	if pr.request.Webhook == "" {
		return nil
	}

	if len(pr.request.WebhookEventsFilter) > 0 && !slices.Contains(pr.request.WebhookEventsFilter, event) {
		return nil
	}

	if event == WebhookLogs || event == WebhookOutput {
		if time.Since(pr.lastUpdated) < 500*time.Millisecond {
			return nil
		}

		pr.lastUpdated = time.Now()
	}

	log := logger.Sugar()
	log.Infow("sending webhook", "url", pr.request.Webhook, "response", pr.response)

	bodyBytes, err := json.Marshal(pr.response)
	if err != nil {
		log.Errorw("failed to marshal pending prediction response", "err", err)

		return err
	}

	req, err := http.NewRequest(http.MethodPost, pr.request.Webhook, bytes.NewBuffer(bodyBytes))
	if err != nil {
		log.Errorw("failed to build pending prediction webhook request", "err", err)

		return err
	}

	req.Header.Add("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Errorw("failed to send webhook", "error", err)

		return err
	} else if resp.StatusCode != 200 {
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Errorw("failed to read pending prediction webhook response body", "err", err)

			return err
		}

		log.Errorw("failed to send pending prediction webhook", "code", resp.StatusCode, "body", string(respBody))
	}

	return nil
}

func (pr *PendingPrediction) sendResponse() {
	if pr.c == nil {
		logger.Warn("attempted pending prediction response with nil channel")

		return
	}

	pr.c <- &pr.response
}
