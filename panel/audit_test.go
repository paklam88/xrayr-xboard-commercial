package panel

import (
	"encoding/json"
	"testing"

	"github.com/xtls/xray-core/infra/conf"

	"github.com/XrayR-project/XrayR/service/controller"
)

func TestBuildAuditRules(t *testing.T) {
	rules := buildAuditRules()
	if len(rules) < 4 {
		t.Fatalf("expected at least 4 audit rules, got %d", len(rules))
	}
}

func TestInjectAuditRoutingPrepends(t *testing.T) {
	existing := mustRawJSON(map[string]any{
		"type":        "field",
		"outboundTag": "IPv4_out",
		"network":     "tcp,udp",
	})
	cfg := &conf.RouterConfig{RuleList: []json.RawMessage{existing}}
	before := len(cfg.RuleList)
	injectAuditRouting(cfg)
	if len(cfg.RuleList) <= before {
		t.Fatal("audit rules should be prepended")
	}
	if string(cfg.RuleList[len(cfg.RuleList)-1]) != string(existing) {
		t.Fatal("existing rule should remain at the end")
	}
	if cfg.DomainStrategy == nil || *cfg.DomainStrategy != "IPOnDemand" {
		t.Fatal("DomainStrategy should default to IPOnDemand")
	}
}

func TestEnsureBlockOutbound(t *testing.T) {
	out := ensureBlockOutbound(nil)
	if len(out) != 2 {
		t.Fatalf("expected freedom+block, got %+v", out)
	}
	if out[0].Protocol != "freedom" || out[1].Tag != "block" {
		t.Fatalf("unexpected outbound order: %+v", out)
	}
	out2 := ensureBlockOutbound(out)
	if len(out2) != 2 {
		t.Fatal("should not duplicate audit outbounds")
	}
}

func TestAnyNodeEnableAudit(t *testing.T) {
	if anyNodeEnableAudit(nil) {
		t.Fatal("nil nodes should be false")
	}
	nodes := []*NodesConfig{{
		ControllerConfig: &controller.Config{EnableAudit: true},
	}}
	if !anyNodeEnableAudit(nodes) {
		t.Fatal("expected true")
	}
}
