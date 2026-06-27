package management

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/usagestats"
)

// GetUsageStats returns aggregated token/latency usage stats (Phase 2).
// Query params: window (1h|6h|24h|48h), bucket (1m|5m|1h), group (provider|model|credential|apikey).
func (h *Handler) GetUsageStats(c *gin.Context) {
	agg := usagestats.GetAggregator()
	window := parseStatsWindow(c.Query("window"))
	bucketSec := parseStatsBucket(c.Query("bucket"), window)
	group := c.DefaultQuery("group", "model")
	resp := agg.BuildResponse(window, bucketSec, group, time.Now())
	c.JSON(http.StatusOK, resp)
}

func parseStatsWindow(s string) time.Duration {
	switch s {
	case "1h":
		return time.Hour
	case "6h":
		return 6 * time.Hour
	case "48h":
		return 48 * time.Hour
	default:
		return 24 * time.Hour
	}
}

func parseStatsBucket(s string, window time.Duration) int {
	switch s {
	case "1m":
		return 60
	case "5m":
		return 300
	case "1h":
		return 3600
	}
	switch {
	case window >= 24*time.Hour:
		return 3600
	case window >= 6*time.Hour:
		return 300
	default:
		return 60
	}
}
