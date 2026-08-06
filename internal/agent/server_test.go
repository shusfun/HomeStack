package agent

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wangshangbin/homestack/internal/protocol"
)

func TestExpiredConfigBlocksTicketRedemption(t *testing.T) {
	server := &Server{configStore: &ConfigStore{current: protocol.SignedDeviceConfigV1{
		Revision: 1, ExpiresAt: time.Now().UTC().Add(-time.Minute),
	}}}
	request := httptest.NewRequest(http.MethodGet, "/access?ticket=unused", nil)
	response := httptest.NewRecorder()
	server.redeemTicket(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("过期配置应返回 %d，实际为 %d", http.StatusServiceUnavailable, response.Code)
	}
}
