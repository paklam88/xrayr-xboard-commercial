package panel

import (
	"encoding/json"

	"github.com/xtls/xray-core/infra/conf"
)

const auditBlockTag = "block"

// Common public BitTorrent trackers blocked when EnableAudit is on.
var defaultAuditTrackers = []string{
	"tracker.openbittorrent.com",
	"tracker.opentrackr.org",
	"open.stealth.si",
	"explodie.org",
	"tracker.torrent.eu.org",
	"tracker.moeking.me",
	"tracker.tiny-vps.com",
	"tracker.bitsearch.to",
	"tracker.bittor.pw",
	"tracker1.bt.moack.co.kr",
	"tracker.leechers-paradise.org",
	"tracker.coppersurfer.tk",
	"tracker.internetwarriors.net",
	"retracker.local",
}

func anyNodeEnableAudit(nodes []*NodesConfig) bool {
	for _, n := range nodes {
		if n != nil && n.ControllerConfig != nil && n.ControllerConfig.EnableAudit {
			return true
		}
	}
	return false
}

func mustRawJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// buildAuditRules returns mandatory local audit routing rules targeting blackhole.
func buildAuditRules() []json.RawMessage {
	return []json.RawMessage{
		mustRawJSON(map[string]any{
			"type":        "field",
			"outboundTag": auditBlockTag,
			"protocol":    []string{"bittorrent"},
		}),
		mustRawJSON(map[string]any{
			"type":        "field",
			"outboundTag": auditBlockTag,
			"port":        "25,465,587",
		}),
		mustRawJSON(map[string]any{
			"type":        "field",
			"outboundTag": auditBlockTag,
			"ip":          []string{"geoip:private"},
		}),
		mustRawJSON(map[string]any{
			"type":        "field",
			"outboundTag": auditBlockTag,
			"domain":      defaultAuditTrackers,
		}),
	}
}

// injectAuditRouting prepends audit rules so they take precedence over user routes.
func injectAuditRouting(router *conf.RouterConfig) {
	if router == nil {
		return
	}
	if router.DomainStrategy == nil {
		strategy := "IPOnDemand"
		router.DomainStrategy = &strategy
	}
	audit := buildAuditRules()
	router.RuleList = append(audit, router.RuleList...)
}

// ensureBlockOutbound appends a blackhole outbound tagged "block" when missing.
// When no freedom outbound exists yet, also inject a direct freedom outbound so
// unmatched traffic is not accidentally sunk into blackhole at core start.
func ensureBlockOutbound(outbounds []conf.OutboundDetourConfig) []conf.OutboundDetourConfig {
	hasBlock := false
	hasFreedom := false
	for _, o := range outbounds {
		if o.Tag == auditBlockTag {
			hasBlock = true
		}
		if o.Protocol == "freedom" {
			hasFreedom = true
		}
	}
	if !hasFreedom {
		outbounds = append([]conf.OutboundDetourConfig{{
			Protocol: "freedom",
			Tag:      "direct",
		}}, outbounds...)
	}
	if !hasBlock {
		outbounds = append(outbounds, conf.OutboundDetourConfig{
			Protocol: "blackhole",
			Tag:      auditBlockTag,
		})
	}
	return outbounds
}
