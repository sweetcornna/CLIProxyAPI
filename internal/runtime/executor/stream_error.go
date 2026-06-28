package executor

import (
	"net/http"
	"strings"
)

func normalizeStreamReadError(provider string, err error) error {
	if err == nil {
		return nil
	}
	if se, ok := err.(interface{ StatusCode() int }); ok && se != nil && se.StatusCode() > 0 {
		return err
	}
	if !isHTTP2InternalStreamReset(err) {
		return err
	}
	provider = strings.TrimSpace(provider)
	if provider == "" {
		provider = "upstream"
	}
	return statusErr{
		code: http.StatusBadGateway,
		msg:  provider + " executor: upstream HTTP/2 stream reset before terminal event (INTERNAL_ERROR)",
	}
}

func isHTTP2InternalStreamReset(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "stream error:") &&
		strings.Contains(msg, "internal_error") &&
		strings.Contains(msg, "received from peer")
}
