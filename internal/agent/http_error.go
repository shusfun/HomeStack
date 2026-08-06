package agent

import (
	"encoding/json"
	"net/http"

	"github.com/wangshangbin/homestack/internal/protocol"
)

func writeAgentError(writer http.ResponseWriter, status int, code, message string) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(protocol.ErrorResponse{Error: protocol.APIError{Code: code, Message: message}})
}
