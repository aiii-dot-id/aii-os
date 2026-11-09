package pluginworker

import (
	"encoding/json"
	"testing"
)

// THE HOST'S OWN DISPATCH FAILURE CARRIES A REASON CODE (the host,
// 2026-11-07): the SDK turns the reply into a denied result, and the
// code names what failed.
func TestTheHostDispatchFailureReplyCarriesItsReasonCode(t *testing.T) {
	var reply struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			ReasonCode string `json:"reasonCode"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(hostDispatchFailedReply), &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Code != -32603 || reply.Message != "host dispatch failed" || reply.Data.ReasonCode != "HOST_DISPATCH_FAILED" {
		t.Fatalf("the reply: %+v", reply)
	}
}
