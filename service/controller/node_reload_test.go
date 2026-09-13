package controller

import (
	"testing"

	"github.com/XrayR-project/XrayR/api"
)

func TestRequiresInboundReload_SpeedLimitOnly(t *testing.T) {
	oldNode := &api.NodeInfo{NodeType: "V2ray", Port: 443, TransportProtocol: "ws", SpeedLimit: 0}
	newNode := &api.NodeInfo{NodeType: "V2ray", Port: 443, TransportProtocol: "ws", SpeedLimit: 100}
	if requiresInboundReload(oldNode, newNode) {
		t.Fatal("speed limit-only change must not reload inbound")
	}
}

func TestRequiresInboundReload_PortChange(t *testing.T) {
	oldNode := &api.NodeInfo{NodeType: "V2ray", Port: 443, TransportProtocol: "ws"}
	newNode := &api.NodeInfo{NodeType: "V2ray", Port: 8443, TransportProtocol: "ws"}
	if !requiresInboundReload(oldNode, newNode) {
		t.Fatal("port change must reload inbound")
	}
}

func TestRequiresInboundReload_TLSChange(t *testing.T) {
	oldNode := &api.NodeInfo{NodeType: "V2ray", Port: 443, EnableTLS: false}
	newNode := &api.NodeInfo{NodeType: "V2ray", Port: 443, EnableTLS: true}
	if !requiresInboundReload(oldNode, newNode) {
		t.Fatal("TLS change must reload inbound")
	}
}
