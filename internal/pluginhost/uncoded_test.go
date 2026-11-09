package pluginhost

import "testing"

// A PLUGIN'S ERROR OR FAILED RESULT WITH NO CODE IS CODED PLUGIN_UNCODED;
// one with a code keeps it, and
// the words stay the plugin's.
func TestAnUncodedPluginErrorIsCodedSo(t *testing.T) {
	for _, c := range []struct{ reply, code, text string }{
		{`{"jsonrpc":"2.0","id":"h1","error":{"code":-32000,"message":"disk full"}}`, ReasonPluginUncoded, "plugin error -32000: disk full"},
		{`{"jsonrpc":"2.0","id":"h1","error":{"code":-32000,"message":"disk full","data":{"reasonCode":"STORE_FULL"}}}`, "STORE_FULL", "plugin error -32000: disk full (reasonCode STORE_FULL)"},
		{`{"jsonrpc":"2.0","id":"h1","result":{"status":"failed","reason":"no room"}}`, ReasonPluginUncoded, "operation failed: no room"},
		{`{"jsonrpc":"2.0","id":"h1","result":{"status":"denied"}}`, ReasonPluginUncoded, "operation denied: no reason given"},
		{`{"jsonrpc":"2.0","id":"h1","result":{"status":"denied","reason":"not now","reasonCode":"BUSY"}}`, "BUSY", "operation denied: not now (BUSY)"},
	} {
		res, err := decodeReply([]byte(c.reply))
		if err != nil {
			t.Fatalf("%s: %v", c.reply, err)
		}
		if res.ReasonCode != c.code || res.Error != c.text {
			t.Errorf("%s:\n code %q text %q\n want %q %q", c.reply, res.ReasonCode, res.Error, c.code, c.text)
		}
	}
}
