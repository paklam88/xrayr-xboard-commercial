// Package limiter is to control the links that go into the dispatcher
package limiter

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/eko/gocache/lib/v4/cache"
	"github.com/eko/gocache/lib/v4/marshaler"
	"github.com/eko/gocache/lib/v4/store"
	goCacheStore "github.com/eko/gocache/store/go_cache/v4"
	redisStore "github.com/eko/gocache/store/redis/v4"
	goCache "github.com/patrickmn/go-cache"
	"github.com/redis/go-redis/v9"
	"github.com/xtls/xray-core/common/errors"
	"golang.org/x/time/rate"

	"github.com/XrayR-project/XrayR/api"
)

const (
	// DefaultDeviceLimitWindow is the sliding window used to tolerate brief multi-IP
	// overlap (e.g. cellular handoff / Wi-Fi reconnect).
	DefaultDeviceLimitWindow = 60 * time.Second
	// DefaultInactiveTTL is how long idle user limiter/stats metadata may remain.
	DefaultInactiveTTL = 24 * time.Hour
)

// UserInfo holds per-user limit metadata for an inbound.
type UserInfo struct {
	UID         int
	SpeedLimit  uint64
	DeviceLimit int
	LastActive  time.Time
}

// deviceIP tracks one client IP inside the sliding window.
type deviceIP struct {
	UID      int
	LastSeen time.Time
}

// InboundInfo is the limiter state for one inbound tag.
type InboundInfo struct {
	Tag               string
	NodeSpeedLimit    uint64
	DeviceLimitWindow time.Duration
	UserInfo          *sync.Map // Key: Email value: UserInfo
	BucketHub         *sync.Map // key: Email, value: *rate.Limiter
	UserOnlineIP      *sync.Map // Key: Email, value: *sync.Map[ip]deviceIP
	GlobalLimit       struct {
		config         *GlobalDeviceLimitConfig
		globalOnlineIP *marshaler.Marshaler
	}
}

// Limiter is the root device/speed limiter.
type Limiter struct {
	InboundInfo       *sync.Map // Key: Tag, Value: *InboundInfo
	DeviceLimitWindow time.Duration
	InactiveTTL       time.Duration
}

// New creates a Limiter with safe defaults.
func New() *Limiter {
	return &Limiter{
		InboundInfo:       new(sync.Map),
		DeviceLimitWindow: DefaultDeviceLimitWindow,
		InactiveTTL:       DefaultInactiveTTL,
	}
}

// Configure overrides sliding-window and inactive GC durations.
func (l *Limiter) Configure(window, inactiveTTL time.Duration) {
	if window > 0 {
		l.DeviceLimitWindow = window
	}
	if inactiveTTL > 0 {
		l.InactiveTTL = inactiveTTL
	}
}

func (l *Limiter) windowDuration(inboundInfo *InboundInfo) time.Duration {
	if inboundInfo != nil && inboundInfo.DeviceLimitWindow > 0 {
		return inboundInfo.DeviceLimitWindow
	}
	if l.DeviceLimitWindow > 0 {
		return l.DeviceLimitWindow
	}
	return DefaultDeviceLimitWindow
}

// AddInboundLimiter replaces limiter state for a tag.
func (l *Limiter) AddInboundLimiter(tag string, nodeSpeedLimit uint64, userList *[]api.UserInfo, globalLimit *GlobalDeviceLimitConfig) error {
	inboundInfo := &InboundInfo{
		Tag:               tag,
		NodeSpeedLimit:    nodeSpeedLimit,
		DeviceLimitWindow: l.DeviceLimitWindow,
		BucketHub:         new(sync.Map),
		UserOnlineIP:      new(sync.Map),
	}

	if globalLimit != nil && globalLimit.Enable {
		inboundInfo.GlobalLimit.config = globalLimit

		gs := goCacheStore.NewGoCache(goCache.New(time.Duration(globalLimit.Expiry)*time.Second, 1*time.Minute))
		rs := redisStore.NewRedis(redis.NewClient(
			&redis.Options{
				Network:  globalLimit.RedisNetwork,
				Addr:     globalLimit.RedisAddr,
				Username: globalLimit.RedisUsername,
				Password: globalLimit.RedisPassword,
				DB:       globalLimit.RedisDB,
			}),
			store.WithExpiration(time.Duration(globalLimit.Expiry)*time.Second))

		cacheManager := cache.NewChain[any](
			cache.New[any](gs),
			cache.New[any](rs),
		)
		inboundInfo.GlobalLimit.globalOnlineIP = marshaler.New(cacheManager)
	}

	now := time.Now()
	userMap := new(sync.Map)
	for _, u := range *userList {
		userMap.Store(fmt.Sprintf("%s|%s|%d", tag, u.Email, u.UID), UserInfo{
			UID:         u.UID,
			SpeedLimit:  u.SpeedLimit,
			DeviceLimit: u.DeviceLimit,
			LastActive:  now,
		})
	}
	inboundInfo.UserInfo = userMap
	l.InboundInfo.Store(tag, inboundInfo)
	return nil
}

// UpdateInboundLimiter updates speed/device limits for the given users.
func (l *Limiter) UpdateInboundLimiter(tag string, updatedUserList *[]api.UserInfo) error {
	value, ok := l.InboundInfo.Load(tag)
	if !ok {
		return fmt.Errorf("no such inbound in limiter: %s", tag)
	}
	inboundInfo := value.(*InboundInfo)
	now := time.Now()
	for _, u := range *updatedUserList {
		email := fmt.Sprintf("%s|%s|%d", tag, u.Email, u.UID)
		prev := UserInfo{LastActive: now}
		if old, exists := inboundInfo.UserInfo.Load(email); exists {
			prev = old.(UserInfo)
		}
		inboundInfo.UserInfo.Store(email, UserInfo{
			UID:         u.UID,
			SpeedLimit:  u.SpeedLimit,
			DeviceLimit: u.DeviceLimit,
			LastActive:  prev.LastActive,
		})
		limit := determineRate(inboundInfo.NodeSpeedLimit, u.SpeedLimit)
		if limit > 0 {
			if bucket, ok := inboundInfo.BucketHub.Load(email); ok {
				limiter := bucket.(*rate.Limiter)
				limiter.SetLimit(rate.Limit(limit))
				limiter.SetBurst(int(limit))
			}
		} else {
			inboundInfo.BucketHub.Delete(email)
		}
	}
	return nil
}

// UpdateNodeSpeedLimit updates the inbound node-level speed limit without rebuild.
func (l *Limiter) UpdateNodeSpeedLimit(tag string, nodeSpeedLimit uint64) error {
	value, ok := l.InboundInfo.Load(tag)
	if !ok {
		return fmt.Errorf("no such inbound in limiter: %s", tag)
	}
	inboundInfo := value.(*InboundInfo)
	inboundInfo.NodeSpeedLimit = nodeSpeedLimit
	inboundInfo.UserInfo.Range(func(key, value interface{}) bool {
		email := key.(string)
		u := value.(UserInfo)
		limit := determineRate(nodeSpeedLimit, u.SpeedLimit)
		if limit > 0 {
			if bucket, ok := inboundInfo.BucketHub.Load(email); ok {
				limiter := bucket.(*rate.Limiter)
				limiter.SetLimit(rate.Limit(limit))
				limiter.SetBurst(int(limit))
			}
		} else {
			inboundInfo.BucketHub.Delete(email)
		}
		return true
	})
	return nil
}

// DeleteInboundLimiter removes all limiter state for a tag.
func (l *Limiter) DeleteInboundLimiter(tag string) error {
	l.InboundInfo.Delete(tag)
	return nil
}

// DeleteUsers removes limiter metadata for deleted panel users (hot-update path).
func (l *Limiter) DeleteUsers(tag string, emails []string) error {
	value, ok := l.InboundInfo.Load(tag)
	if !ok {
		return fmt.Errorf("no such inbound in limiter: %s", tag)
	}
	inboundInfo := value.(*InboundInfo)
	for _, email := range emails {
		inboundInfo.UserInfo.Delete(email)
		inboundInfo.BucketHub.Delete(email)
		inboundInfo.UserOnlineIP.Delete(email)
	}
	return nil
}

// GetOnlineDevice returns IPs still active inside the sliding window.
// Unlike the legacy implementation, it does NOT wipe online state each period.
func (l *Limiter) GetOnlineDevice(tag string) (*[]api.OnlineUser, error) {
	value, ok := l.InboundInfo.Load(tag)
	if !ok {
		return nil, fmt.Errorf("no such inbound in limiter: %s", tag)
	}
	inboundInfo := value.(*InboundInfo)
	now := time.Now()
	window := l.windowDuration(inboundInfo)
	onlineUser := make([]api.OnlineUser, 0)

	inboundInfo.UserOnlineIP.Range(func(key, value interface{}) bool {
		email := key.(string)
		ipMap := value.(*sync.Map)
		active := 0
		ipMap.Range(func(ipKey, ipVal interface{}) bool {
			ip := ipKey.(string)
			dip, ok := ipVal.(deviceIP)
			if !ok {
				ipMap.Delete(ip)
				return true
			}
			if now.Sub(dip.LastSeen) > window {
				ipMap.Delete(ip)
				return true
			}
			onlineUser = append(onlineUser, api.OnlineUser{UID: dip.UID, IP: ip})
			active++
			return true
		})
		if active == 0 {
			inboundInfo.UserOnlineIP.Delete(email)
		}
		return true
	})

	return &onlineUser, nil
}

// GetUserBucket enforces speed + sliding-window device limits.
// device_limit == 0 means unlimited devices.
func (l *Limiter) GetUserBucket(tag string, email string, ip string) (limiter *rate.Limiter, SpeedLimit bool, Reject bool) {
	value, ok := l.InboundInfo.Load(tag)
	if !ok {
		errors.LogDebug(context.Background(), "Get Inbound Limiter information failed")
		return nil, false, false
	}

	var (
		userLimit        uint64
		deviceLimit, uid int
	)
	inboundInfo := value.(*InboundInfo)
	nodeLimit := inboundInfo.NodeSpeedLimit
	now := time.Now()
	window := l.windowDuration(inboundInfo)

	if v, ok := inboundInfo.UserInfo.Load(email); ok {
		u := v.(UserInfo)
		uid = u.UID
		userLimit = u.SpeedLimit
		deviceLimit = u.DeviceLimit
		u.LastActive = now
		inboundInfo.UserInfo.Store(email, u)
	}

	ipMapRaw, _ := inboundInfo.UserOnlineIP.LoadOrStore(email, new(sync.Map))
	ipMap := ipMapRaw.(*sync.Map)
	pruneExpiredIPs(ipMap, now, window)

	if existing, ok := ipMap.Load(ip); ok {
		dip := existing.(deviceIP)
		dip.LastSeen = now
		if dip.UID == 0 {
			dip.UID = uid
		}
		ipMap.Store(ip, dip)
	} else {
		if deviceLimit > 0 && countActiveIPs(ipMap) >= deviceLimit {
			return nil, false, true
		}
		ipMap.Store(ip, deviceIP{UID: uid, LastSeen: now})
	}

	if inboundInfo.GlobalLimit.config != nil && inboundInfo.GlobalLimit.config.Enable {
		if reject := globalLimit(inboundInfo, email, uid, ip, deviceLimit); reject {
			return nil, false, true
		}
	}

	limit := determineRate(nodeLimit, userLimit)
	if limit > 0 {
		newLimiter := rate.NewLimiter(rate.Limit(limit), int(limit))
		if v, ok := inboundInfo.BucketHub.LoadOrStore(email, newLimiter); ok {
			return v.(*rate.Limiter), true, false
		}
		return newLimiter, true, false
	}
	return nil, false, false
}

// CleanupInactive prunes stale IP/bucket state and returns emails idle longer than maxIdle
// so callers can unregister traffic counters. UserInfo for still-valid panel users is kept.
func (l *Limiter) CleanupInactive(tag string, maxIdle time.Duration) []string {
	if maxIdle <= 0 {
		maxIdle = l.InactiveTTL
	}
	if maxIdle <= 0 {
		maxIdle = DefaultInactiveTTL
	}

	value, ok := l.InboundInfo.Load(tag)
	if !ok {
		return nil
	}
	inboundInfo := value.(*InboundInfo)
	now := time.Now()
	window := l.windowDuration(inboundInfo)
	idleEmails := make([]string, 0)

	inboundInfo.UserOnlineIP.Range(func(key, value interface{}) bool {
		email := key.(string)
		ipMap := value.(*sync.Map)
		pruneExpiredIPs(ipMap, now, window)
		if countActiveIPs(ipMap) == 0 {
			inboundInfo.UserOnlineIP.Delete(email)
		}
		return true
	})

	inboundInfo.UserInfo.Range(func(key, value interface{}) bool {
		email := key.(string)
		u := value.(UserInfo)
		if u.LastActive.IsZero() {
			return true
		}
		if now.Sub(u.LastActive) <= maxIdle {
			return true
		}
		if _, online := inboundInfo.UserOnlineIP.Load(email); online {
			return true
		}
		inboundInfo.BucketHub.Delete(email)
		idleEmails = append(idleEmails, email)
		return true
	})

	inboundInfo.BucketHub.Range(func(key, _ interface{}) bool {
		email := key.(string)
		if _, exists := inboundInfo.UserInfo.Load(email); !exists {
			inboundInfo.BucketHub.Delete(email)
		}
		return true
	})

	return idleEmails
}

func pruneExpiredIPs(ipMap *sync.Map, now time.Time, window time.Duration) {
	ipMap.Range(func(key, value interface{}) bool {
		dip, ok := value.(deviceIP)
		if !ok || now.Sub(dip.LastSeen) > window {
			ipMap.Delete(key)
		}
		return true
	})
}

func countActiveIPs(ipMap *sync.Map) int {
	counter := 0
	ipMap.Range(func(_, _ interface{}) bool {
		counter++
		return true
	})
	return counter
}

func globalLimit(inboundInfo *InboundInfo, email string, uid int, ip string, deviceLimit int) bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(inboundInfo.GlobalLimit.config.Timeout)*time.Second)
	defer cancel()

	uniqueKey := strings.Replace(email, inboundInfo.Tag, strconv.Itoa(deviceLimit), 1)

	v, err := inboundInfo.GlobalLimit.globalOnlineIP.Get(ctx, uniqueKey, new(map[string]int))
	if err != nil {
		if _, ok := err.(*store.NotFound); ok {
			go pushIP(inboundInfo, uniqueKey, &map[string]int{ip: uid})
		} else {
			errors.LogErrorInner(context.Background(), err, "cache service")
		}
		return false
	}

	ipMap := v.(*map[string]int)
	if deviceLimit > 0 && len(*ipMap) > deviceLimit {
		return true
	}
	if _, ok := (*ipMap)[ip]; !ok {
		(*ipMap)[ip] = uid
		go pushIP(inboundInfo, uniqueKey, ipMap)
	}
	return false
}

func pushIP(inboundInfo *InboundInfo, uniqueKey string, ipMap *map[string]int) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(inboundInfo.GlobalLimit.config.Timeout)*time.Second)
	defer cancel()
	if err := inboundInfo.GlobalLimit.globalOnlineIP.Set(ctx, uniqueKey, ipMap); err != nil {
		errors.LogErrorInner(context.Background(), err, "cache service")
	}
}

func determineRate(nodeLimit, userLimit uint64) (limit uint64) {
	if nodeLimit == 0 || userLimit == 0 {
		if nodeLimit > userLimit {
			return nodeLimit
		} else if nodeLimit < userLimit {
			return userLimit
		}
		return 0
	}
	if nodeLimit > userLimit {
		return userLimit
	} else if nodeLimit < userLimit {
		return nodeLimit
	}
	return nodeLimit
}
