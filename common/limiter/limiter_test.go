package limiter

import (
	"fmt"
	"testing"
	"time"

	"github.com/XrayR-project/XrayR/api"
)

func TestDeviceLimitZeroUnlimited(t *testing.T) {
	l := New()
	users := []api.UserInfo{{UID: 1, Email: "a@xboard.user", DeviceLimit: 0}}
	if err := l.AddInboundLimiter("tag", 0, &users, nil); err != nil {
		t.Fatal(err)
	}
	email := "tag|a@xboard.user|1"
	for i := 0; i < 5; i++ {
		_, _, reject := l.GetUserBucket("tag", email, fmt.Sprintf("1.1.1.%d", i))
		if reject {
			t.Fatalf("device_limit=0 must not reject, ip index=%d", i)
		}
	}
}

func TestSlidingWindowAllowsHandoff(t *testing.T) {
	l := New()
	l.Configure(2*time.Second, DefaultInactiveTTL)
	users := []api.UserInfo{{UID: 1, Email: "a@xboard.user", DeviceLimit: 1}}
	if err := l.AddInboundLimiter("tag", 0, &users, nil); err != nil {
		t.Fatal(err)
	}
	email := "tag|a@xboard.user|1"

	if _, _, reject := l.GetUserBucket("tag", email, "1.1.1.1"); reject {
		t.Fatal("first IP should be allowed")
	}
	// Overlap within window should reject second IP when limit=1
	if _, _, reject := l.GetUserBucket("tag", email, "2.2.2.2"); !reject {
		t.Fatal("second IP within window must be rejected when device_limit=1")
	}
	// After window expires, old IP is pruned and new IP allowed
	time.Sleep(2100 * time.Millisecond)
	if _, _, reject := l.GetUserBucket("tag", email, "2.2.2.2"); reject {
		t.Fatal("after sliding window, handoff IP should be allowed")
	}
}

func TestGetOnlineDeviceKeepsActiveState(t *testing.T) {
	l := New()
	l.Configure(60*time.Second, DefaultInactiveTTL)
	users := []api.UserInfo{{UID: 1, Email: "a@xboard.user", DeviceLimit: 2}}
	_ = l.AddInboundLimiter("tag", 0, &users, nil)
	email := "tag|a@xboard.user|1"
	_, _, _ = l.GetUserBucket("tag", email, "1.1.1.1")
	_, _, _ = l.GetUserBucket("tag", email, "2.2.2.2")

	online, err := l.GetOnlineDevice("tag")
	if err != nil {
		t.Fatal(err)
	}
	if len(*online) != 2 {
		t.Fatalf("expected 2 online devices, got %d", len(*online))
	}
	// Second call must still see active IPs (no period wipe)
	online2, err := l.GetOnlineDevice("tag")
	if err != nil {
		t.Fatal(err)
	}
	if len(*online2) != 2 {
		t.Fatalf("online state must persist across report cycles, got %d", len(*online2))
	}
}

func TestDeleteUsersClearsState(t *testing.T) {
	l := New()
	users := []api.UserInfo{{UID: 1, Email: "a@xboard.user", DeviceLimit: 1}}
	_ = l.AddInboundLimiter("tag", 0, &users, nil)
	email := "tag|a@xboard.user|1"
	_, _, _ = l.GetUserBucket("tag", email, "1.1.1.1")

	if err := l.DeleteUsers("tag", []string{email}); err != nil {
		t.Fatal(err)
	}
	v, ok := l.InboundInfo.Load("tag")
	if !ok {
		t.Fatal("inbound missing")
	}
	inbound := v.(*InboundInfo)
	if _, exists := inbound.UserInfo.Load(email); exists {
		t.Fatal("UserInfo should be deleted")
	}
	if _, exists := inbound.UserOnlineIP.Load(email); exists {
		t.Fatal("UserOnlineIP should be deleted")
	}
}

func TestCleanupInactive(t *testing.T) {
	l := New()
	users := []api.UserInfo{{UID: 1, Email: "a@xboard.user", DeviceLimit: 1}}
	_ = l.AddInboundLimiter("tag", 0, &users, nil)
	email := "tag|a@xboard.user|1"
	_, _, _ = l.GetUserBucket("tag", email, "1.1.1.1")

	v, _ := l.InboundInfo.Load("tag")
	inbound := v.(*InboundInfo)
	u, _ := inbound.UserInfo.Load(email)
	info := u.(UserInfo)
	info.LastActive = time.Now().Add(-25 * time.Hour)
	inbound.UserInfo.Store(email, info)
	inbound.UserOnlineIP.Delete(email)

	idle := l.CleanupInactive("tag", 24*time.Hour)
	if len(idle) != 1 || idle[0] != email {
		t.Fatalf("expected idle email, got %v", idle)
	}
}
