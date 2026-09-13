package controller

import (
	"encoding/json"
	"reflect"

	"github.com/XrayR-project/XrayR/api"
)

// requiresInboundReload reports whether NodeInfo changes require tearing down the
// listen port. User roster / speed-limit-only changes must NOT reload the inbound.
func requiresInboundReload(oldNode, newNode *api.NodeInfo) bool {
	if oldNode == nil || newNode == nil {
		return true
	}
	if oldNode.NodeType != newNode.NodeType ||
		oldNode.Port != newNode.Port ||
		oldNode.TransportProtocol != newNode.TransportProtocol ||
		oldNode.EnableTLS != newNode.EnableTLS ||
		oldNode.EnableREALITY != newNode.EnableREALITY ||
		oldNode.EnableVless != newNode.EnableVless ||
		oldNode.VlessFlow != newNode.VlessFlow ||
		oldNode.CypherMethod != newNode.CypherMethod ||
		oldNode.ServerKey != newNode.ServerKey ||
		oldNode.Method != newNode.Method ||
		oldNode.Host != newNode.Host ||
		oldNode.Path != newNode.Path ||
		oldNode.ServiceName != newNode.ServiceName ||
		oldNode.Authority != newNode.Authority ||
		oldNode.EnableTFO != newNode.EnableTFO ||
		oldNode.AcceptProxyProtocol != newNode.AcceptProxyProtocol ||
		oldNode.EnableSniffing != newNode.EnableSniffing ||
		oldNode.RouteOnly != newNode.RouteOnly ||
		oldNode.Dest != newNode.Dest ||
		oldNode.ProxyProtocolVer != newNode.ProxyProtocolVer ||
		oldNode.Security != newNode.Security ||
		oldNode.Flow != newNode.Flow ||
		oldNode.Key != newNode.Key ||
		oldNode.PrivateKey != newNode.PrivateKey ||
		oldNode.MinClientVer != newNode.MinClientVer ||
		oldNode.MaxClientVer != newNode.MaxClientVer ||
		oldNode.MaxTimeDiff != newNode.MaxTimeDiff ||
		oldNode.Xver != newNode.Xver ||
		oldNode.RejectUnknownSni != newNode.RejectUnknownSni {
		return true
	}
	if !bytesEqualRaw(oldNode.Header, newNode.Header) {
		return true
	}
	if !reflect.DeepEqual(oldNode.HttpHeaders, newNode.HttpHeaders) ||
		!reflect.DeepEqual(oldNode.Headers, newNode.Headers) ||
		!reflect.DeepEqual(oldNode.ServerNames, newNode.ServerNames) ||
		!reflect.DeepEqual(oldNode.ShortIds, newNode.ShortIds) ||
		!reflect.DeepEqual(oldNode.NameServerConfig, newNode.NameServerConfig) ||
		!reflect.DeepEqual(oldNode.REALITYConfig, newNode.REALITYConfig) {
		return true
	}
	return false
}

func bytesEqualRaw(a, b json.RawMessage) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}
