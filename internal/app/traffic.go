//go:build windows

package app

import (
	"math"
	"net/http"
	"time"
)

type TrafficState struct {
	Available     bool   `json:"available"`
	UploadSpeed   int64  `json:"upload_speed"`
	DownloadSpeed int64  `json:"download_speed"`
	UploadTotal   int64  `json:"upload_total"`
	DownloadTotal int64  `json:"download_total"`
	UpdatedAt     int64  `json:"updated_at"`
	Error         string `json:"error,omitempty"`
}

type clashConnectionsResponse struct {
	UploadTotal   int64 `json:"uploadTotal"`
	DownloadTotal int64 `json:"downloadTotal"`
}

func (a *App) trafficSnapshot() TrafficState {
	if !a.isProcessRunning() {
		a.resetTrafficSnapshot()
		return TrafficState{}
	}

	var payload clashConnectionsResponse
	if err := a.clashAPIRequest(http.MethodGet, "/connections", nil, &payload); err != nil {
		if state, ok := a.cachedTrafficSnapshot(err); ok {
			return state
		}
		return TrafficState{
			Available: false,
			UpdatedAt: time.Now().UnixMilli(),
			Error:     err.Error(),
		}
	}

	now := time.Now()
	state := TrafficState{
		Available:     true,
		UploadTotal:   nonNegativeInt64(payload.UploadTotal),
		DownloadTotal: nonNegativeInt64(payload.DownloadTotal),
		UpdatedAt:     now.UnixMilli(),
	}

	a.trafficMu.Lock()
	defer a.trafficMu.Unlock()

	if a.trafficSampleValid && !a.trafficSampleAt.IsZero() {
		elapsed := now.Sub(a.trafficSampleAt).Seconds()
		if elapsed > 0.2 &&
			state.UploadTotal >= a.trafficUploadTotal &&
			state.DownloadTotal >= a.trafficDownloadTotal {
			state.UploadSpeed = bytesPerSecond(state.UploadTotal-a.trafficUploadTotal, elapsed)
			state.DownloadSpeed = bytesPerSecond(state.DownloadTotal-a.trafficDownloadTotal, elapsed)
		}
	}

	a.trafficUploadTotal = state.UploadTotal
	a.trafficDownloadTotal = state.DownloadTotal
	a.trafficSampleAt = now
	a.trafficSampleValid = true
	return state
}

func (a *App) cachedTrafficSnapshot(sourceErr error) (TrafficState, bool) {
	now := time.Now()
	a.trafficMu.Lock()
	defer a.trafficMu.Unlock()
	if !a.trafficSampleValid || a.trafficSampleAt.IsZero() || now.Sub(a.trafficSampleAt) > 5*time.Second {
		return TrafficState{}, false
	}
	message := ""
	if sourceErr != nil {
		message = sourceErr.Error()
	}
	return TrafficState{
		Available:     true,
		UploadTotal:   a.trafficUploadTotal,
		DownloadTotal: a.trafficDownloadTotal,
		UpdatedAt:     now.UnixMilli(),
		Error:         message,
	}, true
}

func (a *App) resetTrafficSnapshot() {
	a.trafficMu.Lock()
	defer a.trafficMu.Unlock()
	a.trafficUploadTotal = 0
	a.trafficDownloadTotal = 0
	a.trafficSampleAt = time.Time{}
	a.trafficSampleValid = false
}

func bytesPerSecond(delta int64, seconds float64) int64 {
	if delta <= 0 || seconds <= 0 {
		return 0
	}
	value := float64(delta) / seconds
	if value > float64(math.MaxInt64) {
		return math.MaxInt64
	}
	return int64(math.Round(value))
}

func nonNegativeInt64(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}
