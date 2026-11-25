package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"
)

// 2. Local Container Self-Healthcheck (`./server healthcheck`)
func runHealthCheck() error {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}
	pNum, err := strconv.Atoi(port)
	if err != nil || pNum < 1 || pNum > 65535 {
		return errors.New("invalid local health port")
	}

	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			Proxy:       nil,
			DialContext: (&net.Dialer{Timeout: time.Second}).DialContext,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/v1/health", pNum))
	if err != nil {
		return errors.New("local health endpoint unreachable")
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("local health returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return err
	}
	var payload struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.Status != "ok" {
		return errors.New("local health payload not ready")
	}
	return nil
}
