package xboard

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/bitly/go-simplejson"
	"github.com/go-resty/resty/v2"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/infra/conf"

	"github.com/XrayR-project/XrayR/api"
)

const (
	defaultTimeout     = 15 * time.Second
	defaultRetryCount  = 3
	defaultRetryWait   = 1 * time.Second
	defaultRetryMaxWait = 8 * time.Second
)

// APIClient create an api client to the XBoard panel.
type APIClient struct {
	client        *resty.Client
	APIHost       string
	NodeID        int
	Key           string
	NodeType      string
	EnableVless   bool
	VlessFlow     string
	SpeedLimit    float64
	DeviceLimit   int
	LocalRuleList []api.DetectRule
	resp          atomic.Value
	eTags         map[string]string
}

// New create an api instance for XBoard UniProxy.
func New(apiConfig *api.Config) *APIClient {
	client := resty.New()
	client.SetRetryCount(defaultRetryCount)
	client.SetRetryWaitTime(defaultRetryWait)
	client.SetRetryMaxWaitTime(defaultRetryMaxWait)
	client.AddRetryCondition(func(r *resty.Response, err error) bool {
		if err != nil {
			return true
		}
		if r == nil {
			return false
		}
		switch r.StatusCode() {
		case 502, 503, 504:
			return true
		default:
			return false
		}
	})

	if apiConfig.Timeout > 0 {
		client.SetTimeout(time.Duration(apiConfig.Timeout) * time.Second)
	} else {
		client.SetTimeout(defaultTimeout)
	}
	client.OnError(func(req *resty.Request, err error) {
		var v *resty.ResponseError
		if errors.As(err, &v) {
			log.Print(v.Err)
		}
	})
	client.SetBaseURL(apiConfig.APIHost)
	client.SetHeaders(map[string]string{
		"token":         apiConfig.Key,
		"Authorization": apiConfig.Key,
		"Content-Type":  "application/json",
		"Accept":        "application/json",
	})

	var nodeType string
	if apiConfig.NodeType == "V2ray" && apiConfig.EnableVless {
		nodeType = "vless"
	} else {
		nodeType = strings.ToLower(apiConfig.NodeType)
	}
	// Keep query params for XBoard Laravel middleware compatibility.
	client.SetQueryParams(map[string]string{
		"node_id":   strconv.Itoa(apiConfig.NodeID),
		"node_type": nodeType,
		"token":     apiConfig.Key,
	})

	localRuleList := readLocalRuleList(apiConfig.RuleListPath)
	return &APIClient{
		client:        client,
		NodeID:        apiConfig.NodeID,
		Key:           apiConfig.Key,
		APIHost:       apiConfig.APIHost,
		NodeType:      apiConfig.NodeType,
		EnableVless:   apiConfig.EnableVless,
		VlessFlow:     apiConfig.VlessFlow,
		SpeedLimit:    apiConfig.SpeedLimit,
		DeviceLimit:   apiConfig.DeviceLimit,
		LocalRuleList: localRuleList,
		eTags:         make(map[string]string),
	}
}

func readLocalRuleList(path string) (localRuleList []api.DetectRule) {
	localRuleList = make([]api.DetectRule, 0)
	if path == "" {
		return localRuleList
	}

	file, err := os.Open(path)
	if err != nil {
		log.Printf("Error when opening file: %s", err)
		return localRuleList
	}
	defer file.Close()

	fileScanner := bufio.NewScanner(file)
	for fileScanner.Scan() {
		localRuleList = append(localRuleList, api.DetectRule{
			ID:      -1,
			Pattern: regexp.MustCompile(fileScanner.Text()),
		})
	}
	if err := fileScanner.Err(); err != nil {
		log.Printf("Error while reading file: %s", err)
	}
	return localRuleList
}

// Describe return a description of the client
func (c *APIClient) Describe() api.ClientInfo {
	return api.ClientInfo{APIHost: c.APIHost, NodeID: c.NodeID, Key: c.Key, NodeType: c.NodeType}
}

// Debug set the client debug for client
func (c *APIClient) Debug() {
	c.client.SetDebug(true)
}

func (c *APIClient) assembleURL(path string) string {
	return c.APIHost + path
}

func (c *APIClient) parseResponse(res *resty.Response, path string, err error) (*simplejson.Json, error) {
	if err != nil {
		return nil, fmt.Errorf("request %s failed: %v", c.assembleURL(path), err)
	}
	if res == nil {
		return nil, fmt.Errorf("request %s failed: empty response", c.assembleURL(path))
	}
	if res.StatusCode() > 399 {
		return nil, fmt.Errorf("request %s failed: %s", c.assembleURL(path), res.String())
	}

	rtn, err := simplejson.NewJson(res.Body())
	if err != nil {
		return nil, fmt.Errorf("ret %s invalid", res.String())
	}
	return rtn, nil
}

// GetNodeInfo will pull NodeInfo Config from panel
func (c *APIClient) GetNodeInfo() (nodeInfo *api.NodeInfo, err error) {
	server := new(serverConfig)
	path := "/api/v1/server/UniProxy/config"

	res, err := c.client.R().
		SetHeader("If-None-Match", c.eTags["node"]).
		ForceContentType("application/json").
		Get(path)
	if err == nil && res != nil && res.StatusCode() == 304 {
		return nil, errors.New(api.NodeNotModified)
	}
	if res != nil {
		if etag := res.Header().Get("Etag"); etag != "" && etag != c.eTags["node"] {
			c.eTags["node"] = etag
		}
	}

	nodeInfoResp, err := c.parseResponse(res, path, err)
	if err != nil {
		return nil, err
	}
	b, _ := nodeInfoResp.Encode()
	_ = json.Unmarshal(b, server)

	if server.ServerPort == 0 {
		return nil, errors.New("server port must > 0")
	}

	c.resp.Store(server)

	switch c.NodeType {
	case "V2ray", "Vmess", "Vless":
		nodeInfo, err = c.parseV2rayNodeResponse(server)
	case "Trojan":
		nodeInfo, err = c.parseTrojanNodeResponse(server)
	case "Shadowsocks":
		nodeInfo, err = c.parseSSNodeResponse(server)
	default:
		return nil, fmt.Errorf("unsupported node type: %s", c.NodeType)
	}
	if err != nil {
		return nil, fmt.Errorf("parse node info failed: %s, \nError: %v", res.String(), err)
	}
	return nodeInfo, nil
}

// GetUserList will pull user form panel
func (c *APIClient) GetUserList() (*[]api.UserInfo, error) {
	path := "/api/v1/server/UniProxy/user"

	switch c.NodeType {
	case "V2ray", "Trojan", "Shadowsocks", "Vmess", "Vless":
	default:
		return nil, fmt.Errorf("unsupported node type: %s", c.NodeType)
	}

	res, err := c.client.R().
		SetHeader("If-None-Match", c.eTags["users"]).
		ForceContentType("application/json").
		Get(path)
	if err == nil && res != nil && res.StatusCode() == 304 {
		return nil, errors.New(api.UserNotModified)
	}
	if res != nil {
		if etag := res.Header().Get("Etag"); etag != "" && etag != c.eTags["users"] {
			c.eTags["users"] = etag
		}
	}

	usersResp, err := c.parseResponse(res, path, err)
	if err != nil {
		return nil, err
	}

	users, err := extractUsers(usersResp)
	if err != nil {
		return nil, err
	}
	if len(users) == 0 {
		return nil, errors.New("users is null")
	}

	userList := make([]api.UserInfo, len(users))
	for i := 0; i < len(users); i++ {
		u := api.UserInfo{
			UID:  users[i].Id,
			UUID: users[i].Uuid,
		}
		if c.SpeedLimit > 0 {
			u.SpeedLimit = uint64(c.SpeedLimit * 1000000 / 8)
		} else {
			u.SpeedLimit = uint64(users[i].SpeedLimit * 1000000 / 8)
		}
		if c.DeviceLimit > 0 {
			u.DeviceLimit = c.DeviceLimit
		} else {
			u.DeviceLimit = users[i].DeviceLimit
		}
		u.Email = u.UUID + "@xboard.user"
		if c.NodeType == "Shadowsocks" {
			u.Passwd = u.UUID
		}
		userList[i] = u
	}
	return &userList, nil
}

// extractUsers supports XBoard / UniProxy user payload variants:
//  1. {"users":[...]}
//  2. {"data":[...]}
//  3. {"data":{"users":[...]}}
func extractUsers(body *simplejson.Json) ([]*user, error) {
	if body == nil {
		return nil, errors.New("empty user response")
	}

	tryDecode := func(raw json.RawMessage) ([]*user, bool) {
		if len(raw) == 0 || string(raw) == "null" {
			return nil, false
		}
		var users []*user
		if err := json.Unmarshal(raw, &users); err != nil || len(users) == 0 {
			return nil, false
		}
		return users, true
	}

	if raw, err := body.Get("users").Encode(); err == nil {
		if users, ok := tryDecode(raw); ok {
			return users, nil
		}
	}

	dataNode := body.Get("data")
	if raw, err := dataNode.Encode(); err == nil {
		// {"data":[...]}
		if users, ok := tryDecode(raw); ok {
			return users, nil
		}
		// {"data":{"users":[...]}}
		if nested, err := dataNode.Get("users").Encode(); err == nil {
			if users, ok := tryDecode(nested); ok {
				return users, nil
			}
		}
	}

	return nil, errors.New("unable to parse user list from response")
}

// ReportUserTraffic reports the user traffic using XBoard Map payload:
//
//	{"1":[upload,download], "2":[upload,download]}
func (c *APIClient) ReportUserTraffic(userTraffic *[]api.UserTraffic) error {
	path := "/api/v1/server/UniProxy/push"
	data := buildTrafficPayload(userTraffic)

	res, err := c.client.R().
		SetBody(data).
		ForceContentType("application/json").
		Post(path)
	_, err = c.parseResponse(res, path, err)
	return err
}

func buildTrafficPayload(userTraffic *[]api.UserTraffic) map[int][]int64 {
	if userTraffic == nil {
		return map[int][]int64{}
	}
	data := make(map[int][]int64, len(*userTraffic))
	for _, traffic := range *userTraffic {
		data[traffic.UID] = []int64{traffic.Upload, traffic.Download}
	}
	return data
}

// GetNodeRule implements the API interface
func (c *APIClient) GetNodeRule() (*[]api.DetectRule, error) {
	v := c.resp.Load()
	if v == nil {
		ruleList := c.LocalRuleList
		return &ruleList, nil
	}
	routes := v.(*serverConfig).Routes
	ruleList := c.LocalRuleList
	for i := range routes {
		if routes[i].Action == "block" {
			ruleList = append(ruleList, api.DetectRule{
				ID:      i,
				Pattern: regexp.MustCompile(strings.Join(routes[i].Match, "|")),
			})
		}
	}
	return &ruleList, nil
}

// ReportNodeStatus implements the API interface
func (c *APIClient) ReportNodeStatus(nodeStatus *api.NodeStatus) error {
	return nil
}

// ReportNodeOnlineUsers reports alive devices to XBoard UniProxy/alive.
// Payload: {"1":["1.1.1.1","2.2.2.2"]}
func (c *APIClient) ReportNodeOnlineUsers(onlineUserList *[]api.OnlineUser) error {
	data := buildAlivePayload(onlineUserList)
	if len(data) == 0 {
		return nil
	}

	path := "/api/v1/server/UniProxy/alive"
	res, err := c.client.R().
		SetBody(data).
		ForceContentType("application/json").
		Post(path)
	_, err = c.parseResponse(res, path, err)
	return err
}

func buildAlivePayload(onlineUserList *[]api.OnlineUser) map[int][]string {
	data := make(map[int][]string)
	if onlineUserList == nil {
		return data
	}
	seen := make(map[int]map[string]struct{})
	for _, u := range *onlineUserList {
		if u.IP == "" {
			continue
		}
		if _, ok := seen[u.UID]; !ok {
			seen[u.UID] = make(map[string]struct{})
		}
		if _, dup := seen[u.UID][u.IP]; dup {
			continue
		}
		seen[u.UID][u.IP] = struct{}{}
		data[u.UID] = append(data[u.UID], u.IP)
	}
	return data
}

// ReportIllegal implements the API interface
func (c *APIClient) ReportIllegal(detectResultList *[]api.DetectResult) error {
	return nil
}

func (c *APIClient) parseTrojanNodeResponse(s *serverConfig) (*api.NodeInfo, error) {
	return &api.NodeInfo{
		NodeType:          c.NodeType,
		NodeID:            c.NodeID,
		Port:              uint32(s.ServerPort),
		TransportProtocol: "tcp",
		EnableTLS:         true,
		Host:              s.Host,
		ServiceName:       s.ServerName,
		NameServerConfig:  s.parseDNSConfig(),
	}, nil
}

func (c *APIClient) parseSSNodeResponse(s *serverConfig) (*api.NodeInfo, error) {
	var header json.RawMessage
	if s.Obfs == "http" {
		path := "/"
		if p := s.ObfsSettings.Path; p != "" {
			if strings.HasPrefix(p, "/") {
				path = p
			} else {
				path += p
			}
		}
		h := simplejson.New()
		h.Set("type", "http")
		h.SetPath([]string{"request", "path"}, path)
		header, _ = h.Encode()
	}
	return &api.NodeInfo{
		NodeType:          c.NodeType,
		NodeID:            c.NodeID,
		Port:              uint32(s.ServerPort),
		TransportProtocol: "tcp",
		CypherMethod:      s.Cipher,
		ServerKey:         s.ServerKey,
		NameServerConfig:  s.parseDNSConfig(),
		Header:            header,
	}, nil
}

func (c *APIClient) parseV2rayNodeResponse(s *serverConfig) (*api.NodeInfo, error) {
	var (
		host          string
		header        json.RawMessage
		enableTLS     bool
		enableREALITY bool
		dest          string
		xVer          uint64
	)

	if s.VlessTlsSettings.Dest != "" {
		dest = s.VlessTlsSettings.Dest
	} else {
		dest = s.VlessTlsSettings.Sni
	}
	xVer = s.VlessTlsSettings.XVer

	realityConfig := api.REALITYConfig{
		Dest:             dest + ":" + s.VlessTlsSettings.ServerPort,
		ProxyProtocolVer: xVer,
		ServerNames:      []string{s.VlessTlsSettings.Sni},
		PrivateKey:       s.VlessTlsSettings.PrivateKey,
		ShortIds:         []string{s.VlessTlsSettings.ShortId},
	}

	if c.EnableVless {
		s.NetworkSettings = s.VlessNetworkSettings
	}

	switch s.Network {
	case "ws":
		if s.NetworkSettings.Headers != nil {
			httpHeader, err := s.NetworkSettings.Headers.MarshalJSON()
			if err != nil {
				return nil, err
			}
			b, _ := simplejson.NewJson(httpHeader)
			host = b.Get("Host").MustString()
		}
	case "tcp":
		if s.NetworkSettings.Header != nil {
			httpHeader, err := s.NetworkSettings.Header.MarshalJSON()
			if err != nil {
				return nil, err
			}
			header = httpHeader
		}
	case "httpupgrade", "splithttp":
		if s.NetworkSettings.Headers != nil {
			httpHeaders, err := s.NetworkSettings.Headers.MarshalJSON()
			if err != nil {
				return nil, err
			}
			b, _ := simplejson.NewJson(httpHeaders)
			host = b.Get("Host").MustString()
		}
		if s.NetworkSettings.Host != "" {
			host = s.NetworkSettings.Host
		}
	}

	switch s.Tls {
	case 0:
		enableTLS = false
		enableREALITY = false
	case 1:
		enableTLS = true
		enableREALITY = false
	case 2:
		enableTLS = true
		enableREALITY = true
	}

	return &api.NodeInfo{
		NodeType:          c.NodeType,
		NodeID:            c.NodeID,
		Port:              uint32(s.ServerPort),
		AlterID:           0,
		TransportProtocol: s.Network,
		EnableTLS:         enableTLS,
		Path:              s.NetworkSettings.Path,
		Host:              host,
		EnableVless:       c.EnableVless,
		VlessFlow:         s.VlessFlow,
		ServiceName:       s.NetworkSettings.ServiceName,
		Header:            header,
		EnableREALITY:     enableREALITY,
		REALITYConfig:     &realityConfig,
		NameServerConfig:  s.parseDNSConfig(),
	}, nil
}

func (s *serverConfig) parseDNSConfig() (nameServerList []*conf.NameServerConfig) {
	for i := range s.Routes {
		if s.Routes[i].Action == "dns" {
			nameServerList = append(nameServerList, &conf.NameServerConfig{
				Address: &conf.Address{Address: net.ParseAddress(s.Routes[i].ActionValue)},
				Domains: s.Routes[i].Match,
			})
		}
	}
	return
}
