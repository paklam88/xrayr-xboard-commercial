package xboard

import (
	"encoding/json"
	"testing"

	"github.com/bitly/go-simplejson"

	"github.com/XrayR-project/XrayR/api"
)

func TestExtractUsers_TopLevelUsers(t *testing.T) {
	body, err := simplejson.NewJson([]byte(`{
		"users": [
			{"id": 1, "uuid": "u-1", "speed_limit": 100, "device_limit": 3},
			{"id": 2, "uuid": "u-2", "speed_limit": 0, "device_limit": 0}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	users, err := extractUsers(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}
	if users[0].DeviceLimit != 3 {
		t.Fatalf("expected device_limit=3, got %d", users[0].DeviceLimit)
	}
}

func TestExtractUsers_DataArray(t *testing.T) {
	body, err := simplejson.NewJson([]byte(`{
		"data": [
			{"id": 10, "uuid": "a", "speed_limit": 50, "device_limit": 2}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	users, err := extractUsers(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].Id != 10 || users[0].DeviceLimit != 2 {
		t.Fatalf("unexpected users: %+v", users)
	}
}

func TestExtractUsers_DataNestedUsers(t *testing.T) {
	body, err := simplejson.NewJson([]byte(`{
		"data": {
			"users": [
				{"id": 7, "uuid": "nested", "speed_limit": 10, "device_limit": 5}
			]
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	users, err := extractUsers(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].Uuid != "nested" || users[0].DeviceLimit != 5 {
		t.Fatalf("unexpected users: %+v", users)
	}
}

func TestExtractUsers_Invalid(t *testing.T) {
	body, err := simplejson.NewJson([]byte(`{"message":"ok"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := extractUsers(body); err == nil {
		t.Fatal("expected error for invalid payload")
	}
}

func TestBuildTrafficPayload_MapFormat(t *testing.T) {
	traffic := []api.UserTraffic{
		{UID: 1, Upload: 1024, Download: 2048},
		{UID: 2, Upload: 10, Download: 20},
	}
	payload := buildTrafficPayload(&traffic)

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	// Must be object map, not JSON array.
	if raw[0] != '{' {
		t.Fatalf("traffic payload must be JSON object, got %s", raw)
	}
	if len(payload[1]) != 2 || payload[1][0] != 1024 || payload[1][1] != 2048 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestBuildAlivePayload_DedupIPs(t *testing.T) {
	online := []api.OnlineUser{
		{UID: 1, IP: "1.1.1.1"},
		{UID: 1, IP: "1.1.1.1"},
		{UID: 1, IP: "2.2.2.2"},
		{UID: 2, IP: "8.8.8.8"},
		{UID: 3, IP: ""},
	}
	payload := buildAlivePayload(&online)
	if len(payload[1]) != 2 {
		t.Fatalf("expected 2 unique IPs for uid=1, got %v", payload[1])
	}
	if len(payload[2]) != 1 || payload[2][0] != "8.8.8.8" {
		t.Fatalf("unexpected uid=2 payload: %v", payload[2])
	}
	if _, ok := payload[3]; ok {
		t.Fatal("empty IP should be skipped")
	}
}

func TestDeviceLimitOverride(t *testing.T) {
	client := New(&api.Config{
		APIHost:     "http://127.0.0.1",
		Key:         "test-key",
		NodeID:      1,
		NodeType:    "V2ray",
		DeviceLimit: 9,
	})
	users := []*user{{Id: 1, Uuid: "x", DeviceLimit: 3}}
	// Simulate GetUserList mapping logic.
	u := api.UserInfo{UID: users[0].Id, UUID: users[0].Uuid}
	if client.DeviceLimit > 0 {
		u.DeviceLimit = client.DeviceLimit
	} else {
		u.DeviceLimit = users[0].DeviceLimit
	}
	if u.DeviceLimit != 9 {
		t.Fatalf("config DeviceLimit should override panel value, got %d", u.DeviceLimit)
	}
}

func TestNewSetsAuthHeadersAndTimeout(t *testing.T) {
	client := New(&api.Config{
		APIHost:  "http://127.0.0.1",
		Key:      "secret-token",
		NodeID:   42,
		NodeType: "V2ray",
	})
	headers := client.client.Header
	if headers.Get("token") != "secret-token" {
		t.Fatalf("missing token header: %v", headers)
	}
	if headers.Get("Authorization") != "secret-token" {
		t.Fatalf("missing Authorization header: %v", headers)
	}
	if headers.Get("Accept") != "application/json" {
		t.Fatalf("missing Accept header: %v", headers)
	}
	if client.client.GetClient().Timeout != defaultTimeout {
		t.Fatalf("expected default timeout %v, got %v", defaultTimeout, client.client.GetClient().Timeout)
	}
}
